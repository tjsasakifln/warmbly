package confenge

import (
	"fmt"
	"strings"
	"testing"
)

func TestApplyCopyHygienePTBRCurrencyOCRCapsVocativeSender(t *testing.T) {
	cur := ApplyCopyHygiene("obra de R$ 2,839,000 em Caxias")
	if strings.Contains(cur, "2,839,000") {
		t.Fatalf("US amount survived: %q", cur)
	}
	if !strings.Contains(cur, "2.839.000") && !strings.Contains(cur, "2,8") {
		t.Fatalf("PT-BR amount missing: %q", cur)
	}

	ocr := ApplyCopyHygiene("EMPRESA S  ESPECIALIZADA S deengenharia. Contratação  de  empresa  especializada..")
	if strings.Contains(ocr, "deengenharia") || strings.Contains(ocr, "..") {
		t.Fatalf("OCR/spacing not cleaned: %q", ocr)
	}
	if strings.Contains(ocr, "  ") {
		t.Fatalf("double space survived: %q", ocr)
	}

	caps := ApplyCopyHygiene("CONTRATACAO DE EMPRESA ESPECIALIZADA PARA PAVIMENTACAO")
	if caps == strings.ToUpper(caps) {
		t.Fatalf("edital CAPS survived: %q", caps)
	}

	voc := ApplyCopyHygiene("Encopav Engenharia LTDA EIRELI-EPP EM RECUPERACAO JUDICIAL")
	low := strings.ToLower(voc)
	if strings.Contains(low, "ltda") || strings.Contains(low, "eireli") || strings.Contains(low, "recuperacao") || strings.Contains(low, "recuperação") {
		t.Fatalf("legal vocative survived: %q", voc)
	}

	sender := ensureEmailSender("Ola, Ana. Posso te mandar o recorte?")
	if !strings.Contains(sender, "Tiago") || !strings.Contains(sender, "CONFENGE") {
		t.Fatalf("sender missing: %q", sender)
	}

	fmt.Printf("COPY_HYGIENE currency=%q ocr=%q caps=%q vocative=%q sender_ok=true\n", cur, ocr, caps, voc)
}
