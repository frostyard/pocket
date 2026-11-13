package app

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestInsecureSignInConfirmationRule(t *testing.T) {
	for _, tc := range []struct {
		link string
		warn bool
	}{
		{"http://example.org/login?token=test", true},
		{"http://192.0.2.1/login?token=test", true},
		{"http://[2001:db8::1]/login?token=test", true},
		{"http://127.0.0.1/login?token=test", false},
		{"http://127.1.2.3:8899/login?token=test", false},
		{"http://[::1]/login?token=test", false},
		{"http://[::ffff:127.0.0.1]/login?token=test", false},
		{"http://localhost:8899/join/example", false},
		{"http://10.1.2.3/join/example", false},
		{"http://172.16.1.1/join/example", false},
		{"http://192.168.1.1/join/example", false},
		{"http://[fd00::1]/join/example", false},
		{"https://example.org/login?token=test", false},
		{"http://127.0.0.1.example.org/login?token=test", true},
		{"http://localhost@example.org/login?token=test", true},
		{"not a link", false},
	} {
		if got := insecureSignIn(tc.link); got != tc.warn {
			t.Errorf("insecureSignIn(%q)=%v, want %v", tc.link, got, tc.warn)
		}
	}
}

func TestPublicHTTPSignInWaitsForConfirmationSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	a.link.client = nil
	a.signIn.link = "http://example.org/login?token=example"
	a.startSignIn()
	if a.signIn.busy || a.signIn.confirmLink != a.signIn.link {
		t.Fatal("public HTTP sign-in did not wait for confirmation")
	}
	tt := ui.NewTesterWith(a.View, 900, 600, ui.TesterOptions{})
	tt.Frame()
	if !tt.HasText("This server does not use HTTPS: the sign-in token travels in clear text.") || !tt.HasText("Connect anyway") {
		t.Fatal("missing HTTP warning and explicit confirmation")
	}
	writePNG(t, tt, "signin-http-warning")
	tt.Click("Cancel")
	tt.Frame()
	if field, ok := tt.Access("Sign-in link"); !ok || !field.Password {
		t.Fatalf("sign-in link is not masked: found=%v, field=%+v", ok, field)
	}
	if a.signIn.confirmLink != "" || a.signIn.busy {
		t.Fatal("cancel started sign-in or retained confirmation")
	}
}
