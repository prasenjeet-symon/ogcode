package tool

import "testing"

func TestIsDangerousCommand_Blocks(t *testing.T) {
	dangerous := []string{
		"rm -rf /",
		"rm -rf /*",
		"rm -fr /",
		"rm -r -f /",
		"rm --recursive --force /",
		"rm -rf ~",
		"rm -rf ~/",
		"rm -rf $HOME",
		"rm -rf /etc",
		"rm -rf /usr",
		"sudo rm -rf /",
		"echo hi && rm -rf /",
		"true; rm -rf ~/",
		":(){ :|:& };:",
		"mkfs.ext4 /dev/sda1",
		"dd if=/dev/zero of=/dev/sda",
		"chmod -R 000 /",
		"cat foo > /dev/sda",
	}
	for _, c := range dangerous {
		if bad, reason := isDangerousCommand(c); !bad {
			t.Errorf("expected BLOCK for %q, got allowed", c)
		} else if reason == "" {
			t.Errorf("blocked %q but reason was empty", c)
		}
	}
}

func TestIsDangerousCommand_AllowsNormalDevCommands(t *testing.T) {
	safe := []string{
		"",
		"ls -la",
		"go build ./...",
		"go test ./...",
		"npm install",
		"npm run build",
		"rm -rf node_modules",
		"rm -rf ./build",
		"rm -rf dist",
		"rm -rf /tmp/ogcode-scratch",
		"rm -rf ./.cache",
		"rm file.txt",
		"rm -f main.o",
		"git rm -rf internal/old",
		"chmod +x script.sh",
		"chmod -R 755 ./public",
		"dd if=input.img of=output.img",
		"grep -rf pattern .",
		"find . -name '*.go' | xargs rm",
		"echo '> /dev/null cleanup'",
	}
	for _, c := range safe {
		if bad, reason := isDangerousCommand(c); bad {
			t.Errorf("expected ALLOW for %q, got blocked (%s)", c, reason)
		}
	}
}
