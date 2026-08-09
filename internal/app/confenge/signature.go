package confenge

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Signature image is embedded as CID for HTML clients; plain text has the text block only.
const (
	SignatureImageCID      = "tiago-sasaki-signature@confenge"
	SignatureImageFilename = "tiago-sasaki-assinatura.jpeg"
	// Env path override; default walks repo-relative assets.
	EnvSignatureImagePath = "CONFENGE_SIGNATURE_IMAGE_PATH"
)

// Professional PT-BR plain signature for Tiago / CONFENGE commercial email.
const SignaturePlainBlock = `Atenciosamente,

Eng. Tiago Sasaki
CONFENGE
tiago.sasaki@confenge.com.br`

var (
	sigOnce sync.Once
	sigJPEG []byte
	sigErr  error
)

// LoadSignatureJPEG returns the signature image bytes (cached).
func LoadSignatureJPEG() ([]byte, error) {
	sigOnce.Do(func() {
		candidates := []string{}
		if p := strings.TrimSpace(os.Getenv(EnvSignatureImagePath)); p != "" {
			candidates = append(candidates, p)
		}
		// Repo root / data / assets (local WSL + deploy layouts).
		candidates = append(candidates,
			"Tiago Sasaki assinatura.jpeg",
			filepath.Join("data", "confenge", "tiago-sasaki-assinatura.jpeg"),
			filepath.Join("assets", "confenge", "tiago-sasaki-assinatura.jpeg"),
		)
		if wd, err := os.Getwd(); err == nil {
			candidates = append(candidates,
				filepath.Join(wd, "Tiago Sasaki assinatura.jpeg"),
				filepath.Join(wd, "data", "confenge", "tiago-sasaki-assinatura.jpeg"),
			)
		}
		for _, p := range candidates {
			b, err := os.ReadFile(p)
			if err == nil && len(b) > 0 {
				sigJPEG = b
				return
			}
			if err != nil {
				sigErr = err
			}
		}
		if len(sigJPEG) == 0 && sigErr == nil {
			sigErr = fmt.Errorf("CONFENGE signature image not found (set %s)", EnvSignatureImagePath)
		}
	})
	if len(sigJPEG) == 0 {
		return nil, sigErr
	}
	return sigJPEG, nil
}

// SignaturePlain returns the plain-text signature block (no image).
func SignaturePlain() string { return SignaturePlainBlock }

// SignatureHTML returns HTML signature with CID image reference (and optional base64 fallback).
// Prefer CID when the transport attaches LoadSignatureJPEG with Content-ID SignatureImageCID.
func SignatureHTML() string {
	var b strings.Builder
	b.WriteString(`<div style="margin-top:16px;font-family:Arial,Helvetica,sans-serif;font-size:13px;color:#1e293b;line-height:1.45">`)
	b.WriteString(`<p style="margin:0 0 8px 0">Atenciosamente,</p>`)
	b.WriteString(`<p style="margin:0"><strong>Eng. Tiago Sasaki</strong><br>CONFENGE<br>`)
	b.WriteString(`<a href="mailto:tiago.sasaki@confenge.com.br" style="color:#0369a1;text-decoration:none">tiago.sasaki@confenge.com.br</a></p>`)
	// CID first (inline MIME). Base64 data URI as progressive enhancement for sinks that strip CID.
	b.WriteString(fmt.Sprintf(
		`<p style="margin:12px 0 0 0"><img src="cid:%s" alt="Assinatura Tiago Sasaki" width="400" style="max-width:100%%;height:auto;border:0;display:block" /></p>`,
		SignatureImageCID,
	))
	if jpeg, err := LoadSignatureJPEG(); err == nil && len(jpeg) > 0 {
		// Hidden fallback for clients that ignore CID but allow data URI (rare).
		_ = base64.StdEncoding.EncodeToString(jpeg)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// AppendSignaturePlain adds the text signature if not already present.
func AppendSignaturePlain(body string) string {
	body = strings.TrimRight(body, " \t\r\n")
	if strings.Contains(body, "Tiago Sasaki") && strings.Contains(body, "CONFENGE") {
		return body
	}
	return body + "\n\n" + SignaturePlain()
}

// BodyToHTML wraps plain PT-BR body as simple HTML paragraphs and appends signature.
func BodyToHTML(plainBody string) string {
	plain := strings.TrimSpace(plainBody)
	// Strip existing plain signature so we only render once in HTML.
	if i := strings.LastIndex(plain, "Atenciosamente"); i > 0 {
		// keep body; signature re-added as HTML
	}
	// If body already ends with our plain signature, remove it before HTML conversion.
	if idx := strings.LastIndex(plain, SignaturePlainBlock); idx >= 0 {
		plain = strings.TrimSpace(plain[:idx])
	}
	// Strip short draft closings; full signature block is appended as HTML.
	for _, tail := range []string{
		"\n\nAbraço,\nTiago Sasaki\nCONFENGE",
		"\n\nAbraco,\nTiago Sasaki\nCONFENGE",
		"\n\nAbraço,\nCONFENGE",
		"\n\nAbraco,\nCONFENGE",
		"\n\n" + SignaturePlainBlock,
	} {
		if strings.HasSuffix(plain, tail) {
			plain = strings.TrimSpace(strings.TrimSuffix(plain, tail))
		}
	}
	paras := strings.Split(plain, "\n\n")
	var b strings.Builder
	b.WriteString(`<div style="font-family:Arial,Helvetica,sans-serif;font-size:14px;color:#0f172a;line-height:1.5">`)
	for _, p := range paras {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// preserve single newlines inside paragraph
		p = strings.ReplaceAll(htmlEscape(p), "\n", "<br>\n")
		b.WriteString("<p style=\"margin:0 0 12px 0\">")
		b.WriteString(p)
		b.WriteString("</p>\n")
	}
	b.WriteString(SignatureHTML())
	b.WriteString(`</div>`)
	return b.String()
}

func htmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	)
	return r.Replace(s)
}
