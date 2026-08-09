package confenge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignaturePlainProfessionalPTBR(t *testing.T) {
	s := SignaturePlain()
	for _, want := range []string{"Atenciosamente", "Tiago Sasaki", "CONFENGE", "tiago.sasaki@confenge.com.br"} {
		if !strings.Contains(s, want) {
			t.Fatalf("plain signature missing %q: %s", want, s)
		}
	}
	// No unaccented amateur closings.
	if strings.Contains(s, "Abraco") || strings.Contains(s, "Ola,") {
		t.Fatalf("plain signature must use professional PT-BR accents: %s", s)
	}
}

func TestBodyToHTMLIncludesCIDSignature(t *testing.T) {
	html := BodyToHTML("Olá Ana,\n\nNotei a prorrogação do contrato.\n\nFaz sentido conversarmos?")
	if !strings.Contains(html, "cid:"+SignatureImageCID) {
		t.Fatalf("HTML must reference signature CID, got: %s", html)
	}
	if !strings.Contains(html, "Atenciosamente") {
		t.Fatal("HTML missing Atenciosamente")
	}
	if !strings.Contains(html, "Olá Ana") {
		t.Fatal("HTML lost body text")
	}
}

func TestLoadSignatureJPEGFromRepoAsset(t *testing.T) {
	// Prefer stable path written next to repo for go-live.
	p := filepath.Join("data", "confenge", "tiago-sasaki-assinatura.jpeg")
	if _, err := os.Stat(p); err != nil {
		// Fall back to workspace root name.
		p = "Tiago Sasaki assinatura.jpeg"
		if _, err := os.Stat(p); err != nil {
			t.Skip("signature image not present in test cwd")
		}
		t.Setenv(EnvSignatureImagePath, p)
	} else {
		t.Setenv(EnvSignatureImagePath, p)
	}
	// Reset cache by re-invoking after env set — package uses sync.Once so
	// only first successful load sticks. If empty, fail.
	b, err := LoadSignatureJPEG()
	if err != nil {
		t.Fatalf("LoadSignatureJPEG: %v", err)
	}
	if len(b) < 1000 {
		t.Fatalf("signature jpeg too small: %d bytes", len(b))
	}
	// JPEG SOI marker
	if b[0] != 0xff || b[1] != 0xd8 {
		t.Fatalf("not a JPEG")
	}
}

func TestAppendSignaturePlainIdempotent(t *testing.T) {
	body := "Olá,\n\nTexto."
	once := AppendSignaturePlain(body)
	twice := AppendSignaturePlain(once)
	if strings.Count(twice, "Tiago Sasaki") != 1 {
		t.Fatalf("signature should appear once, got:\n%s", twice)
	}
}
