package main

import (
	"os"

	"github.com/moveeeax/aws-backup-coverage/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
