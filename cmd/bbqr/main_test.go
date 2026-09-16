package main

import (
	"bytes"
	"encoding/base64"
	"image/gif"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, stdin []byte, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errb bytes.Buffer
	err = run(bytes.NewReader(stdin), &out, &errb, args)
	return out.String(), errb.String(), err
}

// parts returns the non-blank lines of a make text output.
func parts(text string) []string {
	return strings.Fields(text)
}

func TestMakeDecodeText(t *testing.T) {
	data := "the quick brown fox jumps over the lazy dog"
	out, stderr, err := runCLI(t, []byte(data), "make", "-", "-t", "U")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stderr, "A single QR version") {
		t.Fatalf("stderr %q", stderr)
	}
	if lines := strings.Split(out, "\n"); len(lines) != 3 || lines[1] != "" {
		t.Fatalf("text output %q: want part, blank line, end", out)
	}
	decoded, _, err := runCLI(t, []byte(out), "decode")
	if err != nil || decoded != data+"\n" {
		t.Fatalf("decode: %q, %v", decoded, err)
	}
}

func TestDetectAndPrettyPrint(t *testing.T) {
	psbt, err := os.ReadFile("testdata/1in2out.psbt")
	if err != nil {
		t.Fatal(err)
	}
	out, stderr, err := runCLI(t, nil, "make", "testdata/1in2out.psbt", "-v", "20")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stderr, "Detected file type: P -> PSBT\n") {
		t.Fatalf("stderr %q", stderr)
	}
	dec, stderr, err := runCLI(t, []byte(out), "decode")
	if err != nil || stderr != "PSBT File:\n" || dec != base64.StdEncoding.EncodeToString(psbt)+"\n" {
		t.Fatalf("decode PSBT: %q %q %v", dec[:min(len(dec), 40)], stderr, err)
	}
	dec, _, err = runCLI(t, []byte(out), "decode", "--raw")
	if err != nil || dec != string(psbt) {
		t.Fatalf("decode --raw: %d bytes, %v", len(dec), err)
	}

	// A hex transaction is detected, converted to bytes, and printed as hex.
	txn := "0100000001" + strings.Repeat("ab", 40) + "00000000"
	out, stderr, err = runCLI(t, []byte(txn), "make", "-")
	if err != nil || !strings.HasPrefix(stderr, "Detected file type: T -> Transaction") {
		t.Fatalf("hex txn: %q %v", stderr, err)
	}
	dec, stderr, err = runCLI(t, []byte(out), "decode")
	if err != nil || stderr != "Bitcoin Transaction:\n" || dec != txn+"\n" {
		t.Fatalf("decode txn: %q %q %v", dec, stderr, err)
	}

	// JSON is detected and indented; the Python command never managed the detection.
	out, stderr, err = runCLI(t, []byte(`{"a":[1,2],"b":"c"}`), "make", "-")
	if err != nil || !strings.HasPrefix(stderr, "Detected file type: J -> JSON") {
		t.Fatalf("json: %q %v", stderr, err)
	}
	dec, _, err = runCLI(t, []byte(out), "decode")
	if err != nil || dec != "{\n  \"a\": [\n    1,\n    2\n  ],\n  \"b\": \"c\"\n}\n" {
		t.Fatalf("decode json: %q %v", dec, err)
	}

	// Binary is reported, not shown.
	out, _, err = runCLI(t, []byte{0xff, 0xfe, 0, 1, 2, 3, 4}, "make", "-")
	if err != nil {
		t.Fatal(err)
	}
	dec, stderr, err = runCLI(t, []byte(out), "decode")
	if err != nil || dec != "" || stderr != "Binary: 7 bytes of binary data (not shown)\n" {
		t.Fatalf("decode binary: %q %q %v", dec, stderr, err)
	}
}

// TestParity: --parity adds parts, any Count of them decode, options may
// follow the file name as with the Python command.
func TestParity(t *testing.T) {
	data := make([]byte, 3000)
	rng := rand.New(rand.NewSource(7))
	rng.Read(data)
	out, stderr, err := runCLI(t, data, "make", "-", "-e", "2", "-v", "15", "--parity", "2")
	if err != nil {
		t.Fatal(err)
	}
	lines := parts(out)
	count := len(lines) - 2
	if !strings.Contains(stderr, "+ 2 parity, any") {
		t.Fatalf("stderr %q", stderr)
	}
	rng.Shuffle(len(lines), func(i, j int) { lines[i], lines[j] = lines[j], lines[i] })
	dec, _, err := runCLI(t, []byte(strings.Join(lines[:count], "\n")), "decode", "-r")
	if err != nil || dec != string(data) {
		t.Fatalf("decode from %d of %d parts: %v", count, len(lines), err)
	}
	_, _, err = runCLI(t, []byte(strings.Join(lines[:count-1], "\n")), "decode", "-r")
	if err == nil || !strings.Contains(err.Error(), "parts received") {
		t.Fatalf("decode with too few parts: %v", err)
	}

	// Options after the positional, short and long spellings mixed.
	f := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(f, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out2, _, err := runCLI(t, nil, "make", f, "--encoding", "2", "--max-version", "15", "-p", "2")
	if err != nil || out2 != out {
		t.Fatalf("mixed spellings differ: %v", err)
	}
	// --randomize-order keeps the same set of parts.
	out3, _, err := runCLI(t, nil, "make", f, "-e", "2", "-v", "15", "-p", "2", "-r")
	if err != nil || len(parts(out3)) != len(lines) {
		t.Fatalf("randomized: %v", err)
	}
}

func TestImages(t *testing.T) {
	dir := t.TempDir()
	data := make([]byte, 2000)
	rand.New(rand.NewSource(3)).Read(data)
	f := filepath.Join(dir, "data.bin")
	if err := os.WriteFile(f, data, 0o644); err != nil {
		t.Fatal(err)
	}
	// Animated PNG with a frame per part.
	out, _, err := runCLI(t, nil, "make", f, "-v", "12", "-p", "1", "-o", filepath.Join(dir, "anim.png"), "-s", "2")
	if err != nil || !strings.Contains(out, "frames.") {
		t.Fatalf("png: %q %v", out, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "anim.png"))
	if err != nil {
		t.Fatal(err)
	}
	if img, err := png.Decode(bytes.NewReader(b)); err != nil || img.Bounds().Dx() != (65+20)*2 {
		t.Fatalf("apng first frame: %v %v", img, err)
	}
	if n := bytes.Count(b, []byte("fcTL")); n < 2 {
		t.Fatalf("apng has %d frame control chunks", n)
	}
	// Animated GIF with the frame delay.
	if _, _, err := runCLI(t, nil, "make", f, "-v", "12", "-o", filepath.Join(dir, "anim.gif"), "-d", "500"); err != nil {
		t.Fatal(err)
	}
	gf, err := os.Open(filepath.Join(dir, "anim.gif"))
	if err != nil {
		t.Fatal(err)
	}
	g, err := gif.DecodeAll(gf)
	gf.Close()
	if err != nil || len(g.Image) < 2 || g.Delay[0] != 50 || g.LoopCount != 0 {
		t.Fatalf("gif: %d frames, delay %v, %v", len(g.Image), g.Delay, err)
	}
	// SVG: one file per part.
	out, _, err = runCLI(t, nil, "make", f, "-v", "12", "-o", filepath.Join(dir, "code.svg"))
	if err != nil {
		t.Fatal(err)
	}
	created := strings.Count(out, "Created file")
	if svgs, _ := filepath.Glob(filepath.Join(dir, "code-*.svg")); len(svgs) != created || created < 2 {
		t.Fatalf("svg: %d files, %d reported", len(svgs), created)
	}
	// Single part: a plain PNG.
	if _, _, err := runCLI(t, []byte("just one part"), "make", "-", "-t", "U", "-o", filepath.Join(dir, "one.png")); err != nil {
		t.Fatal(err)
	}
	pf, _ := os.Open(filepath.Join(dir, "one.png"))
	if _, err := png.Decode(pf); err != nil {
		t.Fatalf("single png: %v", err)
	}
	pf.Close()
	// Terminal output and an unsupported extension.
	out, _, err = runCLI(t, []byte("terminal"), "make", "-", "-t", "U", "-o", "stdout")
	if err != nil || !strings.Contains(out, "\x1b[40m") {
		t.Fatalf("terminal: %v", err)
	}
	if _, _, err := runCLI(t, []byte("nope nope"), "make", "-", "-o", filepath.Join(dir, "x.bmp")); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported extension: %v", err)
	}
}

func TestTable(t *testing.T) {
	out, _, err := runCLI(t, nil, "table")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Vers | Pixels  | Chars |", " 27  | 125x125 |  2132 |  1062|    1325 |     2650 |     6625 |    13250", " 40  | 177x177 |  4296 |"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table lacks %q:\n%s", want, out)
		}
	}
}

func TestBadUsage(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"bogus"},
		{"make"},
		{"make", "a", "b"},
		{"make", "-", "-t", "psbt"},
		{"make", "-", "-e", "Q"},
		{"make", "-", "-p", "255"},
		{"make", "-", "-s", "0"},
		{"decode", "a", "b"},
	} {
		if _, _, err := runCLI(t, []byte("some data here"), args...); err == nil {
			t.Errorf("%v: expected error", args)
		}
	}
	if _, _, err := runCLI(t, []byte("tiny"), "make", "-"); err == nil || !strings.Contains(err.Error(), "too short") {
		t.Fatalf("short input: %v", err)
	}
	if _, _, err := runCLI(t, []byte("\n\n"), "decode"); err == nil {
		t.Fatal("decode of blank input: expected error")
	}
	for _, cmd := range []string{"make", "decode"} {
		if _, _, err := runCLI(t, nil, cmd, "-h"); err != nil {
			t.Errorf("%s -h: %v", cmd, err)
		}
	}
	if _, _, err := runCLI(t, nil, "help"); err != nil {
		t.Fatal(err)
	}
}
