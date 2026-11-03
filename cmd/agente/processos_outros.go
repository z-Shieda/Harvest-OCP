//go:build !windows

package main

import "inventario/internal/modelo"

func listarProcessos() []modelo.ProcessoMonitor { return nil }
