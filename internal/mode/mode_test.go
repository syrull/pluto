package mode

import "testing"

func TestFromEnvMode(t *testing.T) {
	cases := []struct {
		name string
		mode string
		ctf  string
		want Mode
	}{
		{"unset", "", "", Default},
		{"mode ctf", "ctf", "", CTF},
		{"mode CTF upper", "CTF", "", CTF},
		{"mode other", "code", "", Default},
		{"ctf on", "", "on", CTF},
		{"ctf 1", "", "1", CTF},
		{"ctf true", "", "true", CTF},
		{"ctf yes", "", "yes", CTF},
		{"ctf off", "", "off", Default},
		{"ctf garbage", "", "maybe", Default},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PLUTO_MODE", tc.mode)
			t.Setenv("PLUTO_CTF", tc.ctf)
			if got := FromEnv(); got != tc.want {
				t.Fatalf("FromEnv() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFromArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"none", []string{"pluto"}, false},
		{"flag double dash", []string{"pluto", "--ctf"}, true},
		{"flag single dash", []string{"pluto", "-ctf"}, true},
		{"subcommand", []string{"pluto", "ctf"}, true},
		{"flag not program name", []string{"ctf"}, false},
		{"other flag", []string{"pluto", "--version"}, false},
		{"mixed", []string{"pluto", "update", "ctf"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FromArgs(tc.args); got != tc.want {
				t.Fatalf("FromArgs(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	t.Setenv("PLUTO_MODE", "")
	t.Setenv("PLUTO_CTF", "")
	if got := Resolve([]string{"pluto"}); got != Default {
		t.Fatalf("Resolve default = %q, want %q", got, Default)
	}
	if got := Resolve([]string{"pluto", "--ctf"}); got != CTF {
		t.Fatalf("Resolve --ctf = %q, want %q", got, CTF)
	}
	t.Setenv("PLUTO_MODE", "ctf")
	if got := Resolve([]string{"pluto"}); got != CTF {
		t.Fatalf("Resolve env = %q, want %q", got, CTF)
	}
}

func TestModeHelpers(t *testing.T) {
	if !CTF.IsCTF() {
		t.Fatal("CTF.IsCTF() should be true")
	}
	if Default.IsCTF() {
		t.Fatal("Default.IsCTF() should be false")
	}
	if Mode("").String() != "default" {
		t.Fatalf("empty mode String() = %q, want default", Mode("").String())
	}
	if CTF.String() != "ctf" {
		t.Fatalf("CTF.String() = %q, want ctf", CTF.String())
	}
}
