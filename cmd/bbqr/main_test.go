package main

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	data := []byte("the quick brown fox jumps over the lazy dog")
	var parts, stderr bytes.Buffer
	if err := run(strings.NewReader(string(data)), &parts, &stderr, []string{"encode", "-type", "U"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stderr.String(), "1 part, QR version") {
		t.Fatalf("encode summary %q", stderr.String())
	}
	var out bytes.Buffer
	stderr.Reset()
	if err := run(&parts, &out, &stderr, []string{"decode"}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Fatalf("round trip mismatch: %q", out.Bytes())
	}
	if stderr.String() != "type U, 43 bytes\n" {
		t.Fatalf("decode summary %q", stderr.String())
	}
}

// TestParityRoundTrip: encode -parity adds parts, and decode recovers
// from any Count of them, shuffled, with the rest dropped.
func TestParityRoundTrip(t *testing.T) {
	data := make([]byte, 3000)
	rng := rand.New(rand.NewSource(7))
	rng.Read(data)
	var parts, stderr bytes.Buffer
	if err := run(bytes.NewReader(data), &parts, &stderr, []string{"encode", "-enc", "2", "-maxver", "15", "-parity", "2"}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(parts.String())
	count := len(lines) - 2
	if !strings.Contains(stderr.String(), "+ 2 parity") {
		t.Fatalf("encode summary %q", stderr.String())
	}
	rng.Shuffle(len(lines), func(i, j int) { lines[i], lines[j] = lines[j], lines[i] })
	var out bytes.Buffer
	in := strings.Join(lines[:count], "\n") + "\n\n"
	if err := run(strings.NewReader(in), &out, &bytes.Buffer{}, []string{"decode"}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Fatal("round trip mismatch")
	}
	// One part short is not enough, and says so.
	in = strings.Join(lines[:count-1], "\n") + "\n"
	err := run(strings.NewReader(in), &bytes.Buffer{}, &bytes.Buffer{}, []string{"decode"})
	if err == nil || !strings.Contains(err.Error(), "parts received") {
		t.Fatalf("decode with too few parts: %v", err)
	}
}

func TestBadOptions(t *testing.T) {
	for _, args := range [][]string{
		{"encode", "-type", "psbtx"},
		{"encode", "-enc", "Q"},
		{"encode", "-parity", "255"},
		{"encode", "-parity", "-1"},
		{"encode", "a", "b"},
	} {
		if err := run(strings.NewReader("data"), &bytes.Buffer{}, &bytes.Buffer{}, args); err == nil {
			t.Errorf("%v: expected error", args)
		}
	}
	if err := run(strings.NewReader("\n\n"), &bytes.Buffer{}, &bytes.Buffer{}, []string{"decode"}); err == nil {
		t.Error("decode of blank input: expected error")
	}
}

func TestUsageError(t *testing.T) {
	if err := run(nil, nil, &bytes.Buffer{}, nil); err != errUsage {
		t.Fatalf("got %v, want errUsage", err)
	}
	if err := run(nil, nil, &bytes.Buffer{}, []string{"bogus"}); err == nil {
		t.Fatal("unknown command: expected error")
	}
}

// TestSubcommandHelp: -h prints usage and succeeds instead of exiting
// with an error.
func TestSubcommandHelp(t *testing.T) {
	for _, cmd := range []string{"encode", "decode"} {
		if err := run(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, []string{cmd, "-h"}); err != nil {
			t.Errorf("%s -h: %v", cmd, err)
		}
	}
}
