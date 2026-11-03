//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"

	"inventario/internal/modelo"
)

// listarProcessos: todos os processos com o caminho completo do executável (o agente roda como
// SYSTEM, então enxerga os processos de todos os usuários).
func listarProcessos() []modelo.ProcessoMonitor {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var r []modelo.ProcessoMonitor
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		p := modelo.ProcessoMonitor{Nome: windows.UTF16ToString(pe.ExeFile[:]), PID: pe.ProcessID}
		if h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pe.ProcessID); e == nil {
			buf := make([]uint16, 1024)
			n := uint32(len(buf))
			if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
				p.Caminho = windows.UTF16ToString(buf[:n])
			}
			windows.CloseHandle(h)
		}
		r = append(r, p)
	}
	return r
}
