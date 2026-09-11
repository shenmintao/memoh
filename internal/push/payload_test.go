package push

import (
	"strings"
	"testing"
)

func TestSMSAndGenericNotifications(t *testing.T) {
	sms := []byte("{\"sender\":\"10086\",\"message\":\"测试\\n\\\"引号\\\"\\\\路径 😀\",\"timestamp\":\"2026-09-11 22:00:00\",\"local_number\":\"SIM1\"}")
	got, err := Parse("application/json; charset=utf-8", sms, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "测试\n\"引号\"\\路径 😀") || !strings.Contains(got.Text, "接收号码：SIM1") || !strings.HasPrefix(got.EventID, "sms:") {
		t.Fatalf("unexpected notification: %#v", got)
	}
	again, _ := Parse("application/json", sms, "")
	if again.EventID != got.EventID {
		t.Fatal("SMS retry did not preserve dedupe key")
	}
	for _, field := range []string{"text", "content", "message"} {
		n, err := Parse("application/json", []byte("{\""+field+"\":\"NAS 告警\"}"), "event-1")
		if err != nil || n.Text != "NAS 告警" || n.EventID != "event-1" {
			t.Fatalf("generic notification: %#v %v", n, err)
		}
	}
	plain, err := Parse("text/plain; charset=UTF-8", []byte("hello\n世界"), "")
	if err != nil || plain.Text != "hello\n世界" || plain.EventID != "" {
		t.Fatalf("plain: %#v %v", plain, err)
	}
}

func TestPayloadRejectsAmbiguousMalformedAndOversizedMessages(t *testing.T) {
	for _, body := range []string{"{}", "null", "[]", "{\"message\":{}}", "{\"message\":\"a\",\"text\":\"b\"}", "{\"message\":\"\\u0000\"}", "{\"message\":\"broken\njson\"}"} {
		if _, err := Parse("application/json", []byte(body), ""); err == nil {
			t.Errorf("accepted %q", body)
		}
	}
	if _, err := Parse("text/plain", []byte(strings.Repeat("a", MaxTextBytes+1)), ""); err == nil {
		t.Fatal("accepted oversized text")
	}
	if _, err := Parse("text/plain", []byte{0xff}, ""); err == nil {
		t.Fatal("accepted invalid UTF-8")
	}
	if _, err := Parse("text/html", []byte("hi"), ""); err == nil {
		t.Fatal("accepted unsupported content type")
	}
}
