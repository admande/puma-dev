package dev

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/admande/puma-dev/homedir"
	"github.com/kardianos/osext"
	"github.com/vektra/errors"
)

func Setup() error {
	err := os.MkdirAll(etcDir, 0755)
	if err != nil {
		return err
	}

	var ok bool

	sudo := os.Getenv("SUDO_USER")
	if sudo != "" {
		uid, err1 := strconv.Atoi(os.Getenv("SUDO_UID"))
		gid, err2 := strconv.Atoi(os.Getenv("SUDO_GID"))

		if err1 == nil && err2 == nil {
			fmt.Printf("* Configuring %s to be owned by %s\n", etcDir, sudo)

			err := os.Chown(etcDir, uid, gid)
			if err != nil {
				return err
			}

			err = os.Chmod(etcDir, 0755)
			if err != nil {
				return err
			}

			files, err := ioutil.ReadDir(etcDir)
			if err != nil {
				return err
			}

			for _, fi := range files {
				path := filepath.Join(etcDir, fi.Name())
				fmt.Printf("* Changing '%s' to be owned by %s\n", path, sudo)

				err = os.Chown(path, uid, gid)
				if err != nil {
					return err
				}

				err = os.Chmod(path, 0644)
				if err != nil {
					return err
				}
			}

			ok = true
		}
	}

	if !ok {
		fmt.Printf("* Configuring %s to be world writable\n", etcDir)
		err := os.Chmod(etcDir, 0777)
		if err != nil {
			return err
		}
	}

	return nil
}

func Cleanup() {
	oldSetup := "/Library/LaunchDaemons/io.puma.devsetup.plist"

	exec.Command("launchctl", "unload", oldSetup).Run()
	os.Remove(oldSetup)
	exec.Command("pfctl", "-F", "nat", "-a", "com.apple/250.PumaDevFirewall").Run()

	fmt.Printf("* Expunged old puma dev system rules\n")

	// Fix perms of the LaunchAgent
	uid, err1 := strconv.Atoi(os.Getenv("SUDO_UID"))
	gid, err2 := strconv.Atoi(os.Getenv("SUDO_GID"))

	if err1 == nil && err2 == nil {
		plist := homedir.MustExpand("~/Library/LaunchAgents/io.puma.dev.plist")
		os.Chown(plist, uid, gid)

		fmt.Printf("* Fixed permissions of user LaunchAgent\n")
	}
}

type InstallIntoSystemArgs struct {
	ListenPort         int
	TlsPort            int
	LogfilePath        string
	ApplinkDirPath     string
	LaunchAgentDirPath string
	Domains            string
	Timeout            string
	NoServePublicPaths string
}

func InstallIntoSystem(config *InstallIntoSystemArgs) error {
	if sudo := os.Getenv("SUDO_USER"); sudo != "" {
		return fmt.Errorf("cannot run as superuser")
	}

	err := SetupOurCert()
	if err != nil {
		return err
	}

	binPath, err := osext.Executable()
	if err != nil {
		return errors.Context(err, "calculating executable path")
	}

	fmt.Printf("* Use '%s' as the location of puma-dev\n", binPath)

	var userTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
   <key>Label</key>
   <string>io.puma.dev</string>
   <key>ProgramArguments</key>
   <array>
     <string>%s</string>
     <string>-launchd</string>
     <string>-dir</string>
     <string>%s</string>
     <string>-d</string>
     <string>%s</string>
     <string>-timeout</string>
     <string>%s</string>
     <string>-no-serve-public-paths</string>
     <string>%s</string>
   </array>
   <key>KeepAlive</key>
   <true/>
   <key>RunAtLoad</key>
   <true/>
   <key>Sockets</key>
   <dict>
       <key>Socket</key>
       <dict>
           <key>SockNodeName</key>
           <string>0.0.0.0</string>
           <key>SockServiceName</key>
           <string>%d</string>
       </dict>
       <key>SocketTLS</key>
       <dict>
           <key>SockNodeName</key>
           <string>0.0.0.0</string>
           <key>SockServiceName</key>
           <string>%d</string>
       </dict>
   </dict>
   <key>StandardOutPath</key>
   <string>%s</string>
   <key>StandardErrorPath</key>
   <string>%s</string>
</dict>
</plist>
`

	logPath := homedir.MustExpand(config.LogfilePath)
	plistDir := homedir.MustExpand(config.LaunchAgentDirPath)
	plist := filepath.Join(plistDir, "io.puma.dev.plist")
	dir := config.ApplinkDirPath

	err = os.MkdirAll(plistDir, 0755)

	if err != nil {
		return errors.Context(err, "creating LaunchAgent directory")
	}

	err = ioutil.WriteFile(
		plist,
		[]byte(fmt.Sprintf(userTemplate, binPath, dir, config.Domains, config.Timeout, config.NoServePublicPaths, config.ListenPort, config.TlsPort, logPath, logPath)),
		0644,
	)

	if err != nil {
		return errors.Context(err, "writing LaunchAgent plist")
	}

	// Unload a previous one if need be.
	// nolint:errcheck
	exec.Command("launchctl", "unload", plist).Run()

	if err = exec.Command("launchctl", "load", plist).Run(); err != nil {
		return errors.Context(err, "loading plist into launchctl")
	}

	fmt.Printf("* Installed puma-dev on ports: http %d, https %d\n", config.ListenPort, config.TlsPort)

	return nil
}

func Uninstall(launchAgentDirPath string, domains []string) func() {
	// Default: ~/Library/LaunchAgents/
	plistDir := homedir.MustExpand(launchAgentDirPath)
	plist := filepath.Join(plistDir, "io.puma.dev.plist")

	// Remove puma-dev daemon
	exec.Command("launchctl", "unload", plist).Run()
	os.Remove(plist)
	fmt.Printf("* Removed puma-dev from automatically running\n")

	for _, d := range domains {
		os.Remove(filepath.Join("/etc/resolver", d))
		fmt.Printf("* Removed domain '%s'\n", d)
	}

	// Remove all `Puma-dev CA` certificate entries from macOS keychain
	if err := DeleteAllPumaDevCAFromDefaultKeychain(); err != nil {
		fmt.Printf("! Unable to remove all Puma-dev CA certs from macOS keychain: %s\n", err)
		return func() {}
	} else {
		fmt.Printf("* Removed all Puma-dev CA certs from macOS keychain.\n")

		return func() {
			os.RemoveAll(homedir.MustExpand(SupportDir))
		}
	}
}
