package cli

import "testing"

func TestAPITokensCreateAddsTokenToDetails(t *testing.T) {
	server := newRecordingServer(t, `{"details":{"id":"t1","token":"old","status":"active"},"token":"secret"}`)
	ta := newTestApp(t)
	ta.addProfile(t, "default", "tok", server.URL)

	out, err := ta.run(t, "api-tokens", "create", "--token-scope", "account")
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"details\": {\n    \"id\": \"t1\",\n    \"token\": \"secret\",\n    \"status\": \"active\"\n  }\n}\n"
	if out != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}
