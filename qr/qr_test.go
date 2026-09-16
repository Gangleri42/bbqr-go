package qr

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type vectors struct {
	VersionBits map[string]string            `json:"version_bits"`
	FormatBits  map[string]map[string]string `json:"format_bits"`
	Cases       []struct {
		Name    string   `json:"name"`
		Text    string   `json:"text"`
		Level   string   `json:"level"`
		Version int      `json:"version"`
		Mode    string   `json:"mode"`
		Mask    int      `json:"mask"`
		SHA256  string   `json:"sha256"`
		Rows    []string `json:"rows"`
	} `json:"cases"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func parseLevel(t *testing.T, s string) Level {
	t.Helper()
	if i := strings.Index("LMQH", s); i >= 0 {
		return Level(i)
	}
	t.Fatalf("bad level %q", s)
	return 0
}

// rows renders the symbol as pyqrcode does for the vectors: rows of 0
// and 1 characters.
func rows(c *Code) []string {
	out := make([]string, c.Size)
	for y := range out {
		var sb strings.Builder
		for x := 0; x < c.Size; x++ {
			if c.Dark(x, y) {
				sb.WriteByte('1')
			} else {
				sb.WriteByte('0')
			}
		}
		out[y] = sb.String()
	}
	return out
}

// TestVectors pins the encoder to pyqrcode's matrices with the mask it
// chose forced, so data encoding, error correction, placement and
// format information are checked cell for cell. Mask choice itself
// follows the standard's scoring, which pyqrcode approximates, so the
// agreement rate is only logged.
func TestVectors(t *testing.T) {
	v := loadVectors(t)
	agree := 0
	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			level := parseLevel(t, c.Level)
			if m := chooseMode(c.Text); m != map[string]mode{"numeric": numeric, "alphanumeric": alphanumeric, "binary": byteMode}[c.Mode] {
				t.Fatalf("mode %d, pyqrcode chose %s", m, c.Mode)
			}
			code, err := encode(c.Text, level, c.Version, c.Mask)
			if err != nil {
				t.Fatal(err)
			}
			got := rows(code)
			sum := sha256.Sum256([]byte(strings.Join(got, "\n")))
			if hex.EncodeToString(sum[:]) != c.SHA256 {
				for i, r := range c.Rows {
					if got[i] != r {
						t.Fatalf("row %d differs:\n got %s\nwant %s", i, got[i], r)
					}
				}
				t.Fatalf("matrix differs from pyqrcode (version %d, level %v, mask %d)", c.Version, level, c.Mask)
			}
			auto, err := Encode(c.Text, level, c.Version)
			if err != nil {
				t.Fatal(err)
			}
			if auto.Mask == c.Mask {
				agree++
			}
		})
	}
	t.Logf("mask choice agrees with pyqrcode in %d of %d cases", agree, len(v.Cases))
}

func TestFormatAndVersionBits(t *testing.T) {
	v := loadVectors(t)
	for lvl, masks := range v.FormatBits {
		for m, want := range masks {
			mask, _ := strconv.Atoi(m)
			if got := strconv.FormatUint(uint64(formatBits(parseLevel(t, lvl), mask)), 2); strings.TrimLeft(want, "0") != got {
				t.Errorf("format bits %s/%d = %s, want %s", lvl, mask, got, want)
			}
		}
	}
	for ver, want := range v.VersionBits {
		n, _ := strconv.Atoi(ver)
		if got := strconv.FormatUint(uint64(versionBits(n)), 2); strings.TrimLeft(want, "0") != got {
			t.Errorf("version bits %d = %s, want %s", n, got, want)
		}
	}
}

func TestVersionChoice(t *testing.T) {
	c, err := Encode("B$HB03000102", L, 0)
	if err != nil || c.Version != 1 || c.Size != 21 {
		t.Fatalf("smallest version: %+v, %v", c, err)
	}
	long := strings.Repeat("A", 4297) // one past version 40 at L
	if _, err := Encode(long, L, 0); !errors.Is(err, ErrTooLong) {
		t.Fatalf("too long for any version: %v", err)
	}
	if _, err := Encode(long[:4296], L, 40); err != nil {
		t.Fatalf("exactly version 40 at L: %v", err)
	}
	if _, err := Encode(long[:4296], L, 39); !errors.Is(err, ErrTooLong) {
		t.Fatalf("too long for version 39: %v", err)
	}
	for _, bad := range []struct {
		text    string
		level   Level
		version int
	}{{"", L, 1}, {"A", Level(4), 1}, {"A", L, 41}, {"A", L, -1}} {
		if _, err := Encode(bad.text, bad.level, bad.version); err == nil {
			t.Errorf("Encode(%q, %d, %d): expected error", bad.text, bad.level, bad.version)
		}
	}
	// Every mask keeps the function patterns and data consistent.
	for k := 0; k < 8; k++ {
		c, err := encode("PARITY", H, 3, k)
		if err != nil || c.Mask != k || !c.Dark(8, c.Size-8) || c.Dark(6, 7) {
			t.Fatalf("mask %d: %+v, %v", k, c, err)
		}
	}
}

// TestZbar decodes rendered symbols with zbarimg when it is installed,
// an oracle independent of pyqrcode that also exercises Image.
func TestZbar(t *testing.T) {
	zbar, err := exec.LookPath("zbarimg")
	if err != nil {
		t.Skip("zbarimg not installed")
	}
	dir := t.TempDir()
	for version := 1; version <= MaxVersion; version += 3 {
		// A BBQr-shaped part filling the version's alphanumeric capacity.
		n := 0
		for bitsNeeded(alphanumeric, n+1, version) <= dataBits(version, L) {
			n++
		}
		text := ("B$HP" + strings.Repeat("0123456789ABCDEF", n/16+1))[:n]
		c, err := Encode(text, L, version)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(dir, "v"+strconv.Itoa(version)+".png")
		f, err := os.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, c.Image(4, 4)); err != nil {
			t.Fatal(err)
		}
		f.Close()
		out, err := exec.Command(zbar, "-q", "--raw", name).Output()
		if err != nil {
			t.Fatalf("version %d: zbarimg: %v", version, err)
		}
		if got := strings.TrimSpace(string(out)); got != text {
			t.Fatalf("version %d: decoded %d chars, want %d", version, len(got), len(text))
		}
	}
}
