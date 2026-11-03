//go:build windows

package main

// Serviço do Windows "InventarioAgente" (1.12): roda como SYSTEM, sempre ativo, mantendo a
// conexão em tempo real com o servidor. Por rodar como SYSTEM, troca o próprio executável em
// C:\Program Files\Inventario sem pedir permissão de administrador. A permissão só é pedida
// UMA vez, na instalação (que cria o serviço); depois disso as atualizações são automáticas.

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const nomeServico = "InventarioAgente"

type servicoAgente struct{}

func (servicoAgente) Execute(_ []string, req <-chan svc.ChangeRequest, st chan<- svc.Status) (bool, uint32) {
	st <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go escutarEventos(ctx)
	go vigiarExecutavel(ctx)
	st <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	registrar("serviço em tempo real iniciado (agente v" + versao + ")")
	for c := range req {
		switch c.Cmd {
		case svc.Interrogate:
			st <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			st <- svc.Status{State: svc.StopPending}
			return false, 0
		}
	}
	return false, 0
}

// rodarServico: chamado com -servico. Pelo gerenciador de serviços, roda como serviço;
// num console (teste manual), roda a escuta em primeiro plano.
func rodarServico() {
	// atualizou: encerra "com falha" e o Windows reinicia o serviço com o .exe novo (ação de
	// recuperação configurada na instalação)
	aoAtualizar = func() {
		registrar("serviço reiniciando para usar a versão nova")
		time.Sleep(2 * time.Second)
		os.Exit(3)
	}
	if ok, _ := svc.IsWindowsService(); ok {
		_ = svc.Run(nomeServico, servicoAgente{})
		return
	}
	escutarEventos(context.Background())
}

// vigiarExecutavel: se a tarefa agendada trocou o executável (auto-update de hora em hora),
// reinicia o serviço para ele também passar a usar a versão nova.
func vigiarExecutavel(ctx context.Context) {
	exe := filepath.Join(dirPrograma(), "inventario-agente.exe")
	ini, err := os.Stat(exe)
	if err != nil {
		return
	}
	t := time.NewTicker(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if fi, err := os.Stat(exe); err == nil && (!fi.ModTime().Equal(ini.ModTime()) || fi.Size() != ini.Size()) {
				aoAtualizar()
			}
		}
	}
}

// instalarServico cria (ou atualiza) o serviço e o inicia. Precisa de administrador/SYSTEM —
// roda na instalação e, nas máquinas já instaladas, pela tarefa agendada (que é SYSTEM).
func instalarServico(exe string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	cfgSvc := mgr.Config{
		DisplayName:      "Inventário Ocupacional - Agente",
		Description:      "Mantém a conexão com o servidor de inventário da TI e aplica as atualizações do agente automaticamente.",
		StartType:        mgr.StartAutomatic,
		DelayedAutoStart: true,
	}
	s, err := m.OpenService(nomeServico)
	if err == nil {
		atual, e := s.Config()
		if e == nil {
			atual.BinaryPathName = `"` + exe + `" -servico`
			atual.StartType = mgr.StartAutomatic
			atual.DelayedAutoStart = true
			atual.ServiceStartName = "LocalSystem"
			atual.DisplayName, atual.Description = cfgSvc.DisplayName, cfgSvc.Description
			_ = s.UpdateConfig(atual)
		}
	} else {
		s, err = m.CreateService(nomeServico, exe, cfgSvc, "-servico")
		if err != nil {
			return err
		}
	}
	defer s.Close()
	// se o serviço parar com erro (ou após atualizar), o Windows o inicia de novo
	_ = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 2 * time.Minute},
	}, 24*60*60)
	_ = s.SetRecoveryActionsOnNonCrashFailures(true)
	if q, err := s.Query(); err == nil && q.State == svc.Stopped {
		return s.Start()
	}
	return nil
}

// pararServico para o serviço (antes de copiar um executável novo por cima).
func pararServico() {
	m, err := mgr.Connect()
	if err != nil {
		return
	}
	defer m.Disconnect()
	s, err := m.OpenService(nomeServico)
	if err != nil {
		return
	}
	defer s.Close()
	if _, err := s.Control(svc.Stop); err != nil {
		return
	}
	for i := 0; i < 20; i++ {
		if q, err := s.Query(); err != nil || q.State == svc.Stopped {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func removerServico() {
	pararServico()
	m, err := mgr.Connect()
	if err != nil {
		return
	}
	defer m.Disconnect()
	if s, err := m.OpenService(nomeServico); err == nil {
		_ = s.Delete()
		s.Close()
	}
}

func contaAtual() string {
	tok := windows.GetCurrentProcessToken()
	u, err := tok.GetTokenUser()
	if err != nil {
		return ""
	}
	sid := u.User.Sid.String()
	if sid == "S-1-5-18" {
		return "SYSTEM"
	}
	if acc, dom, _, err := u.User.Sid.LookupAccount(""); err == nil {
		return dom + `\` + acc
	}
	return sid
}

// podeSeAtualizar confere (sem alterar nada) se a troca do executável funcionaria.
func podeSeAtualizar() (bool, string) {
	if !executandoInstalado() {
		exe, _ := os.Executable()
		return false, "o agente não está rodando de " + dirPrograma() + " (está em " + exe + ")"
	}
	if contaAtual() != "SYSTEM" {
		return false, "o serviço não está rodando como SYSTEM (conta: " + contaAtual() + ")"
	}
	teste := filepath.Join(dirPrograma(), ".teste-escrita")
	f, err := os.Create(teste)
	if err != nil {
		return false, "sem permissão de escrita em " + dirPrograma() + " (antivírus?): " + err.Error()
	}
	f.Close()
	if err := os.Rename(teste, teste+"2"); err != nil {
		_ = os.Remove(teste)
		return false, "o Windows/antivírus bloqueia renomear arquivos em " + dirPrograma() + ": " + err.Error()
	}
	_ = os.Remove(teste + "2")
	return true, ""
}
