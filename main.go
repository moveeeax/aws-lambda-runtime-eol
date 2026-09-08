package main

import (
	"os"

	"github.com/moveeeax/aws-lambda-runtime-eol/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
