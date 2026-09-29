//go:build !windows && !linux && !darwin && !freebsd

package ui

import (
	"errors"
	"os"
)

// On other systems the CLI stays plain: no colors and no arrow-key menus.

func IsTerminal(*os.File) bool    { return false }
func colorTerminal(*os.File) bool { return false }
func EnableVT(*os.File) bool      { return false }
func Width(*os.File) int          { return 0 }

func MakeRaw(*os.File) (func(), error) { return nil, errors.New("não suportado neste sistema") }
