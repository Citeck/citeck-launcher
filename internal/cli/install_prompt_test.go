package cli

import "testing"

func TestDisplayWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"Login:", 6},
		{"Open in browser:", 16},
		{"Откройте в браузере:", 20}, // Cyrillic is narrow
		{"ブラウザで開く:", 15},             // 7 wide + 1 narrow colon
		{"在浏览器中打开：", 16},             // 7 wide + 1 fullwidth colon
		{"登录：", 6},                   // 2 wide + 1 fullwidth colon
	}
	for _, c := range cases {
		if got := displayWidth(c.in); got != c.want {
			t.Errorf("displayWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPadRight(t *testing.T) {
	cases := []struct {
		in    string
		width int
		want  string
		wantW int
	}{
		{"Login:", 16, "Login:          ", 16},
		{"Open in browser:", 16, "Open in browser:", 16},
		{"登录：", 16, "登录：          ", 16}, // pad CJK to match ASCII width
		{"too long already", 5, "too long already", 16},
	}
	for _, c := range cases {
		got := padRight(c.in, c.width)
		if got != c.want {
			t.Errorf("padRight(%q, %d) = %q, want %q", c.in, c.width, got, c.want)
		}
		if w := displayWidth(got); w != c.wantW {
			t.Errorf("padRight(%q, %d) width = %d, want %d", c.in, c.width, w, c.wantW)
		}
	}
}
