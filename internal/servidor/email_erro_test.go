package servidor

import (
	"errors"
	"strings"
	"testing"
)

func TestExplicarErroSMTP(t *testing.T) {
	casos := map[string]string{
		"login no servidor de e-mail recusado: 535 5.7.139 Authentication unsuccessful, SmtpClientAuthentication is disabled for the Tenant": "SMTP autenticado",
		"login no servidor de e-mail recusado: 535 5.7.3 Authentication unsuccessful":                                                        "senha de app",
		"554 5.2.0 STOREDRV.Submission.Exception:SendAsDeniedException; 5.7.60":                                                              "remetente",
		"dial tcp 52.96.0.1:587: i/o timeout":                                                                                                "bloqueada",
		"dial tcp: lookup smtp.ofice365.com: no such host":                                                                                   "não foi encontrado",
		"login no servidor de e-mail recusado: read tcp x: i/o timeout":                                                                      "não respondeu ao login",
	}
	for e, quer := range casos {
		if r := explicarErroSMTP(errors.New(e)); !strings.Contains(r, quer) {
			t.Errorf("%q → %q (queria %q)", e, r, quer)
		}
	}
}
