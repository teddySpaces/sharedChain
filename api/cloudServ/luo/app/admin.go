package app

import (
	"runtime/debug"

	"github.com/teddy/sign-in-on/utils"
)

func ReloadConfig() {
	debug.FreeOSMemory()
	utils.LoadConfig(utils.CfgFileName)
}
