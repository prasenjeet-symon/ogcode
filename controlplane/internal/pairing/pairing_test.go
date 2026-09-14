package pairing

import (
	"testing"
	"time"
)

func TestCheckSecret(t *testing.T) {
	a := New("correct-horse", time.Minute)
	cases := []struct {
		name      string
		presented string
		want      bool
	}{
		{"exact", "correct-horse", true},
		{"wrong", "correct-horse!", false},
		{"prefix", "correct", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := a.CheckSecret(tc.presented); got != tc.want {
				t.Fatalf("CheckSecret(%q) = %v, want %v", tc.presented, got, tc.want)
			}
		})
	}
}

func TestCheckSecretEmptyConfiguredNeverAuthenticates(t *testing.T) {
	a := New("", time.Minute)
	if a.CheckSecret("") {
		t.Fatal("blank configured secret must never authenticate")
	}
	if a.CheckSecret("anything") {
		t.Fatal("blank configured secret must never authenticate")
	}
}

func TestMintUniqueAndExpiring(t *testing.T) {
	a := New("s", 10*time.Minute)
	now := time.Now()
	t1, err := a.Mint(now)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := a.Mint(now)
	if err != nil {
		t.Fatal(err)
	}
	if t1.Value == "" || t2.Value == "" {
		t.Fatal("empty token minted")
	}
	if t1.Value == t2.Value {
		t.Fatal("tokens must be unique")
	}
	if !t1.ExpiresAt.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("expiry: got %v", t1.ExpiresAt)
	}
	if t1.Expired(now) {
		t.Fatal("fresh token must not be expired")
	}
	if !t1.Expired(now.Add(11 * time.Minute)) {
		t.Fatal("token must be expired past TTL")
	}
}

func TestShouldRotate(t *testing.T) {
	a := New("s", 30*time.Minute)
	now := time.Now()
	tok, _ := a.Mint(now)
	if a.ShouldRotate(tok, now) {
		t.Fatal("fresh token should not rotate")
	}
	// Within the last third of life -> rotate.
	if !a.ShouldRotate(tok, now.Add(21*time.Minute)) {
		t.Fatal("token in last third should rotate")
	}
	if !a.ShouldRotate(Token{}, now) {
		t.Fatal("empty token should always rotate")
	}
}
