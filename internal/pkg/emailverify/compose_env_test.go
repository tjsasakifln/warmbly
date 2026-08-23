package emailverify

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// This package's correctness depends on something outside Go: the backend
// CONTAINER must actually receive EMAIL_VERIFY_HELO_HOST / EMAIL_VERIFY_MAIL_FROM.
// The production incident was exactly that gap — the code read both variables,
// the host .env defined both, and the compose file in between propagated
// neither, so the running backend fell back to "localhost" and recorded its own
// HELO refusals against real recipients.
//
// A grep for the literal string would pass on a var that is defined in a stanza
// no service consumes. These tests parse the compose YAML, resolve anchors and
// merge keys the way the compose engine does, and assert the value resolves ON
// THE BACKEND SERVICE.

// composeFiles are the two files that must agree: the canonical root compose
// and the production overlay.
var composeFiles = []struct {
	path    string
	service string
}{
	{"docker-compose.yml", "backend"},
	{filepath.Join("deploy", "confenge-vps", "docker-compose.override.yml"), "backend"},
}

// requiredVerifierEnv must resolve on the backend service to a pure
// interpolation of the same-named variable with an EMPTY default. An empty
// default is load-bearing: a missing value must fail closed (boot refusal in
// production, UNKNOWN elsewhere) rather than invent an identity, and a
// hardcoded default would put a real hostname into a committed file.
var requiredVerifierEnv = []string{EnvHeloHost, EnvMailFrom}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "docker-compose.yml")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate repo root above %s", dir)
		}
		dir = parent
	}
}

// deref follows YAML aliases (*anchor) to the node they point at.
func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// mergeSources returns the mapping nodes referenced by a "<<" merge key, which
// may be a single alias or a sequence of them.
func mergeSources(n *yaml.Node) []*yaml.Node {
	n = deref(n)
	if n == nil {
		return nil
	}
	if n.Kind == yaml.SequenceNode {
		out := make([]*yaml.Node, 0, len(n.Content))
		for _, item := range n.Content {
			if d := deref(item); d != nil && d.Kind == yaml.MappingNode {
				out = append(out, d)
			}
		}
		return out
	}
	if n.Kind == yaml.MappingNode {
		return []*yaml.Node{n}
	}
	return nil
}

// mapValue looks a key up in a mapping, honouring merge keys with the same
// precedence the YAML spec gives them: an explicit key wins over a merged one.
func mapValue(n *yaml.Node, key string) *yaml.Node {
	n = deref(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	var merged []*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Value == "<<" {
			merged = append(merged, mergeSources(v)...)
			continue
		}
		if k.Value == key {
			return v
		}
	}
	for _, m := range merged {
		if got := mapValue(m, key); got != nil {
			return got
		}
	}
	return nil
}

// flattenEnv renders a compose `environment:` block as the engine would, in
// either supported form (mapping or "KEY=value" list) and through merge keys.
func flattenEnv(n *yaml.Node) map[string]string {
	out := map[string]string{}
	n = deref(n)
	if n == nil {
		return out
	}
	if n.Kind == yaml.SequenceNode {
		for _, item := range n.Content {
			if d := deref(item); d != nil && d.Kind == yaml.ScalarNode {
				k, v, _ := strings.Cut(d.Value, "=")
				out[strings.TrimSpace(k)] = v
			}
		}
		return out
	}
	if n.Kind != yaml.MappingNode {
		return out
	}
	var merged []*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Value == "<<" {
			merged = append(merged, mergeSources(v)...)
			continue
		}
		if d := deref(v); d != nil {
			out[k.Value] = d.Value
		}
	}
	for _, m := range merged {
		for mk, mv := range flattenEnv(m) {
			if _, ok := out[mk]; !ok {
				out[mk] = mv
			}
		}
	}
	return out
}

func serviceEnv(t *testing.T, path, service string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(doc.Content) == 0 {
		t.Fatalf("%s is empty", path)
	}
	svc := mapValue(mapValue(doc.Content[0], "services"), service)
	if svc == nil {
		t.Fatalf("%s has no service %q", path, service)
	}
	return flattenEnv(mapValue(svc, "environment"))
}

// TestVerifierIdentityReachesBackendService fails the build if either variable
// stops reaching the backend service in either compose file.
func TestVerifierIdentityReachesBackendService(t *testing.T) {
	root := repoRoot(t)
	for _, cf := range composeFiles {
		env := serviceEnv(t, filepath.Join(root, cf.path), cf.service)
		for _, key := range requiredVerifierEnv {
			val, ok := env[key]
			if !ok {
				t.Fatalf("%s: service %q does not receive %s; the backend container "+
					"would fall back to an unusable prober identity", cf.path, cf.service, key)
			}
			want := regexp.MustCompile(`^\$\{` + regexp.QuoteMeta(key) + `(:-)?\}$`)
			if !want.MatchString(strings.TrimSpace(val)) {
				t.Fatalf("%s: %s on service %q must be %q (empty default so a missing "+
					"value fails closed instead of inventing an identity), got %q",
					cf.path, key, cf.service, "${"+key+":-}", val)
			}
		}
	}
}

// TestComposeFilesCarryNoHardcodedVerifierIdentity: the canonical values live in
// the host .env, never in a committed file. A literal here would both leak an
// operational identity into git and silently paper over a missing .env.
func TestComposeFilesCarryNoHardcodedVerifierIdentity(t *testing.T) {
	root := repoRoot(t)
	for _, cf := range composeFiles {
		raw, err := os.ReadFile(filepath.Join(root, cf.path))
		if err != nil {
			t.Fatalf("read %s: %v", cf.path, err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			for _, key := range requiredVerifierEnv {
				if !strings.HasPrefix(trimmed, key+":") && !strings.HasPrefix(trimmed, "- "+key+"=") {
					continue
				}
				if !strings.Contains(trimmed, "${"+key) {
					t.Fatalf("%s: %s must be an interpolation, not a literal: %q", cf.path, key, trimmed)
				}
				// "${KEY:-something}" would reintroduce an invented identity.
				if idx := strings.Index(trimmed, "${"+key+":-"); idx >= 0 {
					rest := trimmed[idx+len("${"+key+":-"):]
					if !strings.HasPrefix(rest, "}") {
						t.Fatalf("%s: %s must have an EMPTY default so a missing value fails "+
							"closed, got %q", cf.path, key, trimmed)
					}
				}
			}
		}
	}
}

// TestDockerComposeConfigResolvesVerifierIdentity is the belt-and-braces check
// against the real compose engine: it renders the merged root+overlay config and
// asserts our probe value lands on the backend. YAML parsing above is the
// load-bearing assertion; this only runs where docker exists.
func TestDockerComposeConfigResolvesVerifierIdentity(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available; TestVerifierIdentityReachesBackendService is the primary assertion")
	}
	root := repoRoot(t)
	const probeHelo = "compose-probe.example.invalid"
	const probeFrom = "probe@compose-probe.example.invalid"

	cmd := exec.Command("docker", "compose",
		"-f", "docker-compose.yml",
		"-f", filepath.Join("deploy", "confenge-vps", "docker-compose.override.yml"),
		"config")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		EnvHeloHost+"="+probeHelo,
		EnvMailFrom+"="+probeFrom,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("docker compose config unavailable in this environment (%v); YAML assertions still apply:\n%s", err, out)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("parse docker compose config output: %v", err)
	}
	if len(doc.Content) == 0 {
		t.Fatal("docker compose config produced no document")
	}
	backend := mapValue(mapValue(doc.Content[0], "services"), "backend")
	if backend == nil {
		t.Fatal("rendered config has no backend service")
	}
	env := flattenEnv(mapValue(backend, "environment"))
	if env[EnvHeloHost] != probeHelo {
		t.Fatalf("rendered backend %s = %q, want %q", EnvHeloHost, env[EnvHeloHost], probeHelo)
	}
	if env[EnvMailFrom] != probeFrom {
		t.Fatalf("rendered backend %s = %q, want %q", EnvMailFrom, env[EnvMailFrom], probeFrom)
	}
}
