package server

import (
	"net/url"
	"testing"
)

func TestPushReceiverAuthExceptionAndLogRedaction(t *testing.T) {
	id := "d74b5339-1586-4ac3-adf0-fb1e58baf600"
	if !shouldSkipJWT("/push/" + id) {
		t.Fatal("receiver needs scoped key authentication")
	}
	for _, path := range []string{"/push/", "/push/not-an-id", "/push/" + id + "/extra", "/users/me/push-endpoints", "/users/me/push-endpoints/" + id} {
		if shouldSkipJWT(path) {
			t.Fatalf("unprotected sibling %s", path)
		}
	}
	u, _ := url.Parse("/push/" + id + "?key=secret&message=private")
	if got := safeRequestLogURI(u, u.RequestURI()); got != "/push/"+id {
		t.Fatalf("query leaked: %s", got)
	}
}
