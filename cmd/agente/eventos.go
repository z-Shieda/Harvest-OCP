package main

// Conexão em tempo real com o servidor (1.12): o serviço do agente mantém um GET aberto em
// /api/v1/eventos (Server-Sent Events). Eventos:
//   versao → se o servidor distribui uma versão mais nova, baixa e troca o executável na hora;
//   teste  → responde ao painel se esta máquina aceitaria a atualização (sem atualizar nada).
// Se a conexão cair, reconecta sozinho; o envio de hora em hora pela tarefa agendada continua
// como plano B.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"inventario/internal/modelo"
)

// aoAtualizar roda depois que o executável foi trocado (no serviço do Windows: encerra com
// erro e o Windows reinicia o serviço já com o executável novo).
var aoAtualizar = func() {}

var atualizacaoPendente atomic.Bool

func idDoAgente() string {
	var a arquivoID
	if b, err := os.ReadFile(filepath.Join(dirDados(), "agent_id.json")); err == nil {
		_ = json.Unmarshal(b, &a)
	}
	return a.AgentID
}

func nomeMaquina() string {
	h, _ := os.Hostname()
	return strings.ToUpper(h)
}

// escutarEventos fica conectado até o contexto acabar.
func escutarEventos(ctx context.Context) {
	espera := 5 * time.Second
	falhando := false
	for ctx.Err() == nil {
		cfg := carregarConfig() // relê a cada conexão (o token pode ter mudado)
		inicio := time.Now()
		err := conectarEventos(ctx, cfg, func() {
			if falhando {
				registrar("conexão em tempo real restabelecida com " + cfg.Servidor)
				falhando = false
			}
		})
		if ctx.Err() != nil {
			return
		}
		if novo := carregarConfig().Servidor; !strings.EqualFold(novo, cfg.Servidor) {
			registrar("conexão em tempo real passando para o servidor novo: " + novo)
			espera, falhando = 5*time.Second, false
			continue // conecta já no servidor novo
		}
		if time.Since(inicio) > 2*time.Minute {
			espera = 5 * time.Second // ficou conectado um bom tempo: reconecta rápido
		}
		if !falhando && err != nil {
			registrar("conexão em tempo real caiu (" + err.Error() + "); tentando de novo")
			falhando = true
		}
		// espera com variação aleatória: evita todas as máquinas reconectando no mesmo segundo
		t := espera + time.Duration(rand.Int63n(int64(espera)))
		select {
		case <-ctx.Done():
			return
		case <-time.After(t):
		}
		if espera < 2*time.Minute {
			espera *= 2
		}
	}
}

func conectarEventos(ctx context.Context, cfg Config, conectado func()) error {
	if cfg.Servidor == "" || cfg.Token == "" {
		return errors.New("servidor/token não configurados")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.Servidor, "/")+"/api/v1/eventos", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Agente-Token", cfg.Token)
	req.Header.Set("X-Agente-ID", idDoAgente())
	req.Header.Set("X-Agente-Host", nomeMaquina())
	req.Header.Set("X-Agente-Versao", versao)
	cli := &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 30 * time.Second}}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("servidor respondeu %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	conectado()
	// vigia: o servidor manda "ping" a cada 25 s; 90 s sem nada = conexão morta
	vivo := make(chan struct{}, 1)
	go func() {
		t := time.NewTimer(90 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-vivo:
				if !t.Stop() {
					<-t.C
				}
				t.Reset(90 * time.Second)
			case <-t.C:
				cancel()
				return
			}
		}
	}()
	leitor := bufio.NewReader(resp.Body)
	var nome string
	var dados bytes.Buffer
	for {
		linha, err := leitor.ReadString('\n')
		if err != nil {
			if ctx.Err() != nil && errors.Is(context.Cause(ctx), context.Canceled) {
				return errors.New("sem sinal do servidor")
			}
			return err
		}
		select {
		case vivo <- struct{}{}:
		default:
		}
		linha = strings.TrimRight(linha, "\r\n")
		switch {
		case linha == "":
			if nome != "" {
				tratarEvento(cfg, nome, dados.Bytes(), cancel)
			}
			nome = ""
			dados.Reset()
		case strings.HasPrefix(linha, ":"):
			// comentário (ping)
		case strings.HasPrefix(linha, "event:"):
			nome = strings.TrimSpace(linha[6:])
		case strings.HasPrefix(linha, "data:"):
			if dados.Len() > 0 {
				dados.WriteByte('\n')
			}
			dados.WriteString(strings.TrimSpace(linha[5:]))
		}
	}
}

func tratarEvento(cfg Config, nome string, dados []byte, reconectar func()) {
	switch nome {
	case "migrar":
		// o servidor antigo pede para passar ao novo: confere se o novo aceita e troca o config
		var m modelo.Migracao
		if json.Unmarshal(dados, &m) != nil || m.Servidor == "" {
			return
		}
		go func() {
			rel, err := coletarCompleto()
			if err != nil {
				return
			}
			migrarServidor(cfg, m, rel)
			if !strings.EqualFold(carregarConfig().Servidor, cfg.Servidor) {
				reconectar() // conecta de novo, agora no servidor novo
			}
		}()
	case "versao":
		var ev modelo.EventoVersao
		if json.Unmarshal(dados, &ev) != nil || !ev.Atualizar || !versaoMenor(versao, ev.Versao) {
			return
		}
		if !atualizacaoPendente.CompareAndSwap(false, true) {
			return // já tem uma atualização a caminho
		}
		go func() {
			defer atualizacaoPendente.Store(false)
			// espalha os downloads (até 90 s) para não baixarem todas ao mesmo tempo
			time.Sleep(time.Duration(rand.Int63n(int64(90 * time.Second))))
			registrar("tempo real: servidor distribui a v" + ev.Versao + "; atualizando")
			if autoAtualizar(cfg, modelo.Atualizacao{Versao: ev.Versao}) {
				aoAtualizar()
			}
		}()
	case "teste":
		var ev modelo.EventoTeste
		if json.Unmarshal(dados, &ev) != nil || ev.ID == "" {
			return
		}
		go responderTeste(cfg, ev)
	}
}

// responderTeste confere, sem alterar nada, se a atualização funcionaria nesta máquina.
func responderTeste(cfg Config, ev modelo.EventoTeste) {
	r := modelo.RespostaTeste{TesteID: ev.ID, AgentID: idDoAgente(), Hostname: nomeMaquina(), Versao: versao, Usuario: contaAtual()}
	pode, motivo := podeSeAtualizar()
	r.PodeAtualizar, r.Motivo = pode, motivo
	if pode {
		switch {
		case !versaoMenor(versao, ev.Versao):
			r.JaAtual = true
			r.Motivo = "já está na v" + versao
		case !ev.Atualizar:
			r.Motivo = "pronta para a v" + ev.Versao + ", mas o auto-update está desligado no servidor"
		default:
			r.Motivo = "pronta: trocaria da v" + versao + " para a v" + ev.Versao + " sem pedir permissão"
		}
	}
	b, _ := json.Marshal(r)
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(cfg.Servidor, "/")+"/api/v1/eventos/resposta", bytes.NewReader(b))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agente-Token", cfg.Token)
	cli := &http.Client{Timeout: 30 * time.Second}
	if resp, err := cli.Do(req); err == nil {
		resp.Body.Close()
	}
}
