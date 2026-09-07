package mail

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizerDecodesSanitizesAndDescribesAttachments(t *testing.T) {
	raw := strings.Join([]string{
		"From: =?UTF-8?Q?Example_Sender?= <sender@example.test>",
		"To: Owner <owner@example.test>",
		"Subject: =?UTF-8?Q?Safe_=E2=9C=93?=",
		"Message-ID: <child@example.test>",
		"References: <root@example.test> <parent@example.test>",
		"In-Reply-To: <parent@example.test>",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=outer",
		"",
		"--outer",
		"Content-Type: multipart/alternative; boundary=inner",
		"",
		"--inner",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"Hello =E2=9C=93",
		"--inner",
		"Content-Type: text/html; charset=utf-8",
		"",
		`<div onclick="steal()">Hello <strong>safe</strong><script>steal()</script><a href="javascript:steal()">bad</a><a href="https://example.test">good</a><img src="https://tracker.example/pixel"></div>`,
		"--inner--",
		"--outer",
		`Content-Type: application/pdf; name="report.pdf"`,
		`Content-Disposition: attachment; filename="report.pdf"`,
		"Content-Transfer-Encoding: base64",
		"Content-ID: <document@example.test>",
		"",
		"cGRm",
		"--outer--",
		"",
	}, "\r\n")
	normalizer, err := NewNormalizer(DefaultMIMEPolicy())
	if err != nil {
		t.Fatalf("create normalizer: %v", err)
	}
	content, err := normalizer.Normalize(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("normalize MIME: %v", err)
	}
	if content.Subject != "Safe ✓" || content.MessageID != "child@example.test" || content.BodyText != "Hello ✓" {
		t.Fatalf("normalized headers/body = subject %q, id %q, text %q", content.Subject, content.MessageID, content.BodyText)
	}
	if len(content.References) != 2 || len(content.InReplyTo) != 1 || len(content.Addresses) != 2 {
		t.Fatalf("normalized relationships = references %v, replies %v, addresses %+v", content.References, content.InReplyTo, content.Addresses)
	}
	for _, forbidden := range []string{"<script", "onclick=", "javascript:", "<img", "tracker.example"} {
		if strings.Contains(strings.ToLower(content.BodyHTML), forbidden) {
			t.Fatalf("sanitized HTML contains %q: %s", forbidden, content.BodyHTML)
		}
	}
	if !strings.Contains(content.BodyHTML, `href="https://example.test"`) || !strings.Contains(content.BodyHTML, `rel="noopener noreferrer"`) {
		t.Fatalf("sanitized safe link missing: %s", content.BodyHTML)
	}
	if len(content.Attachments) != 1 || content.Attachments[0].Filename != "report.pdf" || content.Attachments[0].MediaType != "application/pdf" || content.Attachments[0].SizeBytes != 3 {
		t.Fatalf("attachment descriptor = %+v", content.Attachments)
	}

	input := UpsertMessageInput{}
	if err := content.Apply(&input); err != nil || input.BodyHTML != content.BodyHTML || len(input.Attachments) != 1 {
		t.Fatalf("apply content: input=%+v error=%v", input, err)
	}
}

func TestNormalizerRejectsMalformedOrUnboundedMIMEWithoutPartialContent(t *testing.T) {
	policy := DefaultMIMEPolicy()
	policy.MaxRawBytes = 128
	normalizer, err := NewNormalizer(policy)
	if err != nil {
		t.Fatalf("create normalizer: %v", err)
	}
	content, err := normalizer.Normalize(strings.NewReader("Subject: private\r\n\r\n" + strings.Repeat("x", 256)))
	if !errors.Is(err, ErrMIMETooLarge) || !reflect.DeepEqual(content, NormalizedMessageContent{}) {
		t.Fatalf("oversized result = %+v, %v", content, err)
	}

	policy.MaxRawBytes = 4096
	normalizer, _ = NewNormalizer(policy)
	content, err = normalizer.Normalize(strings.NewReader("Content-Type: multipart/mixed; boundary=missing\r\n\r\n--missing\r\nContent-Type: text/plain\r\n\r\npartial"))
	if !errors.Is(err, ErrMalformedMIME) || !reflect.DeepEqual(content, NormalizedMessageContent{}) {
		t.Fatalf("malformed result = %+v, %v", content, err)
	}

	policy.MaxParts = 1
	normalizer, _ = NewNormalizer(policy)
	content, err = normalizer.Normalize(strings.NewReader("Content-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain\r\n\r\nbody\r\n--x--\r\n"))
	if !errors.Is(err, ErrTooManyParts) || !reflect.DeepEqual(content, NormalizedMessageContent{}) {
		t.Fatalf("part-limited result = %+v, %v", content, err)
	}
}

func TestNormalizerDerivesPlainTextFromSafeHTML(t *testing.T) {
	normalizer, _ := NewNormalizer(DefaultMIMEPolicy())
	content, err := normalizer.Normalize(strings.NewReader("Content-Type: text/html; charset=utf-8\r\n\r\n<p>Hello <b>mail</b></p>"))
	if err != nil || content.BodyText != "Hello mail" || content.BodyHTML != "<p>Hello <b>mail</b></p>" {
		t.Fatalf("HTML fallback = %+v, %v", content, err)
	}
}

func TestSanitizeHTMLAcceptsProviderFragments(t *testing.T) {
	sanitized, err := sanitizeHTML(`<p onclick="private()">Safe text</p><script>private()</script>`)
	if err != nil || sanitized != "<p>Safe text</p>" {
		t.Fatalf("sanitize provider fragment = %q, %v", sanitized, err)
	}
}

func FuzzNormalizerNeverReturnsUnsafePartialContent(f *testing.F) {
	f.Add([]byte("Subject: seed\r\n\r\nhello"))
	f.Add([]byte("Content-Type: text/html\r\n\r\n<script>alert(1)</script><p>safe</p>"))
	f.Add([]byte("Content-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\n"))
	policy := DefaultMIMEPolicy()
	policy.MaxRawBytes = 32 << 10
	policy.MaxPartBytes = 16 << 10
	policy.MaxBodyBytes = 16 << 10
	policy.MaxParts = 32
	normalizer, _ := NewNormalizer(policy)
	f.Fuzz(func(t *testing.T, raw []byte) {
		content, err := normalizer.Normalize(strings.NewReader(string(raw)))
		if err != nil {
			if !reflect.DeepEqual(content, NormalizedMessageContent{}) {
				t.Fatalf("error returned partial content: %+v", content)
			}
			return
		}
		lower := strings.ToLower(content.BodyHTML)
		for _, forbidden := range []string{"<script", "<iframe", "javascript:", " onerror=", " onclick="} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("successful normalization contains %q", forbidden)
			}
		}
	})
}
