package updater

import "os/exec"

func startProcess(file string) error {
	return exec.Command(file).Start()
}
