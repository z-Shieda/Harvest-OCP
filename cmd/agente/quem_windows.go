//go:build windows

package main

// Usuários logados agora (1.22, evento "quem"): lidos direto da API de sessões do Windows
// (wtsapi32), sem PowerShell — é rápido e não chama atenção do antivírus a cada assinatura.

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	wtsapi32                        = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSQuerySessionInformationW = wtsapi32.NewProc("WTSQuerySessionInformationW")
)

const (
	wtsUserName   = 5
	wtsDomainName = 7
)

func infoSessao(id uint32, classe int) string {
	var buf *uint16
	var n uint32
	r, _, _ := procWTSQuerySessionInformationW.Call(0, uintptr(id), uintptr(classe), uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&n)))
	if r == 0 || buf == nil {
		return ""
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(buf)))
	return strings.TrimSpace(windows.UTF16PtrToString(buf))
}

// usuariosLogados: DOMINIO\usuario de cada sessão ativa (console e área de trabalho remota).
func usuariosLogados() []string {
	var sessoes *windows.WTS_SESSION_INFO
	var qtd uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &sessoes, &qtd); err != nil {
		return nil
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(sessoes)))
	lista := unsafe.Slice(sessoes, qtd)
	vistos := map[string]bool{}
	var r []string
	for _, s := range lista {
		if s.State != windows.WTSActive {
			continue
		}
		u := infoSessao(s.SessionID, wtsUserName)
		if u == "" {
			continue
		}
		if d := infoSessao(s.SessionID, wtsDomainName); d != "" {
			u = d + `\` + u
		}
		if k := strings.ToLower(u); !vistos[k] {
			vistos[k] = true
			r = append(r, u)
		}
	}
	return r
}
