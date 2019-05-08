// +build linux

/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package lvm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/containerd/snapshots"
	"github.com/pkg/errors"
)

const retries = 10

func formatVolume(vgname string, lvname string, fstype string) error {
	var mkfsArgs []string
	switch fstype {
	case "ext4":
		mkfsArgs = append(mkfsArgs, "-E", "nodiscard,lazy_itable_init=0,lazy_journal_init=0")
	case "xfs":
		mkfsArgs = append(mkfsArgs, "-K")
	default:
	}

	cmd := "mkfs." + fstype
	mkfsArgs = append(mkfsArgs, filepath.Join("/dev/", vgname, lvname))
	_, err := runCommand(cmd, mkfsArgs)
	return err
}

func createLVMVolume(lock sync.Mutex, lvname string, vgname string, lvpoolname string, size string, parent string, kind snapshots.Kind) (string, error) {
	cmd := "lvcreate"
	args := []string{}
	out := ""
	var err error
	lock.Lock()
	defer lock.Unlock()

	if parent != "" {
		args = append(args, "--name", lvname, "--snapshot", vgname+"/"+parent)
	} else {
		// Create a new logical volume without a base snapshot
		args = append(args, "--virtualsize", size, "--name", lvname, "--thin", vgname+"/"+lvpoolname)
	}

	// This change will prevent the volume from being mountable. Relying on the
	// mount command to do read-only mounting.
	//if kind == snapshots.KindView {
	//	args = append(args, "-pr")
	//}

	//Let's go and create the volume
	if out, err = runCommand(cmd, args); err != nil {
		return out, errors.Wrap(err, "Unable to create volume")
	}
	return out, err
}

func removeLVMVolume(lock sync.Mutex, lvname string, vgname string) (string, error) {
	lock.Lock()
	defer lock.Unlock()

	// Unmount volume from the system
	cmd := "umount"
	args := []string{"--lazy", "--force", "--all-targets", filepath.Join("/dev", vgname, lvname)}

	output, err := runCommand(cmd, args)
	if err != nil && !strings.Contains(output, "not mounted") {
		return output, errors.Wrap(err, "Unable to unmount volume")
	}
	cmd = "lvremove"
	args = []string{"-y", vgname + "/" + lvname}

	return runCommand(cmd, args)
}

func createVolumeGroup(lock sync.Mutex, drive string, vgname string) (string, error) {
	lock.Lock()
	defer lock.Unlock()
	cmd := "vgcreate"
	args := []string{vgname, drive}

	return runCommand(cmd, args)
}

func createLogicalThinPool(lock sync.Mutex, vgname string, lvpool string) (string, error) {
	lock.Lock()
	defer lock.Unlock()
	cmd := "lvcreate"
	args := []string{"--thinpool", lvpool, "--extents", "90%FREE", vgname}

	out, err := runCommand(cmd, args)
	if err != nil && (err.Error() == "exit status 5") {
		return out, nil
	}
	return out, err
}

func deleteVolumeGroup(lock sync.Mutex, vgname string) (string, error) {
	lock.Lock()
	defer lock.Unlock()
	cmd := "vgremove"
	args := []string{"-y", vgname}

	return runCommand(cmd, args)
}

func checkVG(lock sync.Mutex, vgname string) (string, error) {
	lock.Lock()
	defer lock.Unlock()
	var err error
	output := ""
	cmd := "vgs"
	args := []string{vgname, "--options", "vg_name", "--no-headings"}
	output, err = runCommand(cmd, args)
	return output, err
}

func checkLV(lock sync.Mutex, vgname string, lvname string) (string, error) {
	lock.Lock()
	defer lock.Unlock()
	var err error
	output := ""
	cmd := "lvs"
	args := []string{vgname + "/" + lvname, "--options", "lv_name", "--no-heading"}
	output, err = runCommand(cmd, args)
	return output, err
}

func toggleactivateLV(lock sync.Mutex, vgname string, lvname string, activate bool) (string, error) {
	lock.Lock()
	defer lock.Unlock()
	cmd := "lvchange"
	args := []string{"-K", vgname + "/" + lvname, "-a"}
	output := ""
	var err error

	if activate {
		args = append(args, "y")
	} else {
		args = append(args, "n")
	}
	output, err = runCommand(cmd, args)
	return output, err
}

func toggleactivateVG(lock sync.Mutex, vgname string, activate bool) (string, error) {
	lock.Lock()
	defer lock.Unlock()
	cmd := "vgchange"
	args := []string{"-K", vgname, "-a"}
	output := ""
	var err error

	if activate {
		args = append(args, "y")
	} else {
		args = append(args, "n")
	}
	output, err = runCommand(cmd, args)
	return output, err
}

func runCommand(cmd string, args []string) (string, error) {
	var output []byte
	ret := 0
	var err error

	// Pass context down and log into the tool instead of this.
	// fmt.Printf("Running command %s with args: %s\n", cmd, args)
	for ret < retries {
		c := exec.Command(cmd, args...)
		c.Env = os.Environ()
		c.SysProcAttr = &syscall.SysProcAttr{
			Pdeathsig: syscall.SIGTERM,
			Setpgid:   true,
		}

		output, err = c.CombinedOutput()
		if err == nil {
			break
		}
		ret++
		time.Sleep(100000 * time.Nanosecond)
	}

	return strings.TrimSpace(string(output)), err
}
