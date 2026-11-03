// Aviso de chamado novo (1.10): consulta o servidor a cada 20 s e, quando chega chamado novo,
// toca um som, mostra um aviso no canto da tela, muda o título da aba e — se o navegador
// permitir — mostra a notificação do sistema. O botão de alto-falante no topo liga/desliga.
(function () {
  var CHAVE = 'avisosChamado', ULTIMO = 'avisosUltimoChamado', INTERVALO = 20000;
  var btn = document.getElementById('btn-avisos');
  var tituloOriginal = document.title, naoVistos = 0, ctx = null, ultimo = 0;

  function ler(k) { try { return localStorage.getItem(k); } catch (e) { return null; } }
  function gravar(k, v) { try { localStorage.setItem(k, v); } catch (e) { } }
  function ligado() { return ler(CHAVE) !== '0'; }

  function audio() {
    try {
      ctx = ctx || new (window.AudioContext || window.webkitAudioContext)();
      if (ctx.state === 'suspended') ctx.resume();
    } catch (e) { ctx = null; }
    return ctx;
  }
  // três notas curtas e subindo (sem arquivo de áudio)
  function tocar() {
    var c = audio(); if (!c) return;
    var t = c.currentTime + 0.05;
    [[880, 0], [1175, 0.17], [1568, 0.34]].forEach(function (n) {
      var o = c.createOscillator(), g = c.createGain();
      o.type = 'sine'; o.frequency.value = n[0];
      g.gain.setValueAtTime(0.0001, t + n[1]);
      g.gain.exponentialRampToValueAtTime(0.3, t + n[1] + 0.02);
      g.gain.exponentialRampToValueAtTime(0.0001, t + n[1] + 0.32);
      o.connect(g); g.connect(c.destination);
      o.start(t + n[1]); o.stop(t + n[1] + 0.35);
    });
  }

  function pilha() {
    var p = document.querySelector('.avisos-pilha');
    if (!p) { p = document.createElement('div'); p.className = 'avisos-pilha'; p.setAttribute('role', 'status'); p.setAttribute('aria-live', 'polite'); document.body.appendChild(p); }
    return p;
  }
  function mostrar(c) {
    var d = document.createElement('div'); d.className = 'aviso-chamado';
    var a = document.createElement('a'); a.href = '/chamados/' + c.id;
    var b = document.createElement('b'); b.textContent = (c.para_mim ? 'Chamado para você · #' : 'Novo chamado · #') + c.id + ' (' + c.prioridade + ')';
    var s1 = document.createElement('span'); s1.textContent = c.titulo;
    var s2 = document.createElement('span'); s2.textContent = c.solicitante || '';
    a.appendChild(b); a.appendChild(s1); a.appendChild(s2);
    var x = document.createElement('button'); x.type = 'button'; x.setAttribute('aria-label', 'Fechar aviso'); x.textContent = '×';
    x.onclick = function () { d.remove(); };
    d.appendChild(a); d.appendChild(x);
    pilha().appendChild(d);
    setTimeout(function () { d.remove(); }, 60000);
    if ('Notification' in window && window.isSecureContext && Notification.permission === 'granted') {
      try {
        var n = new Notification('Novo chamado #' + c.id, { body: c.titulo + (c.solicitante ? ' — ' + c.solicitante : ''), tag: 'chamado-' + c.id });
        n.onclick = function () { window.focus(); location.href = '/chamados/' + c.id; };
      } catch (e) { }
    }
  }
  function marcarTitulo(n) {
    naoVistos += n;
    document.title = '(' + naoVistos + ') Novo chamado · ' + tituloOriginal;
  }
  document.addEventListener('visibilitychange', function () {
    if (!document.hidden) { naoVistos = 0; document.title = tituloOriginal; }
  });

  function atualizarBotao() {
    if (!btn) return;
    btn.classList.toggle('desligado', !ligado());
    btn.title = ligado() ? 'Avisos de chamado novo: ligados (clique para desligar)' : 'Avisos de chamado novo: desligados (clique para ligar)';
    btn.setAttribute('aria-pressed', ligado() ? 'true' : 'false');
  }
  if (btn) {
    btn.addEventListener('click', function () {
      gravar(CHAVE, ligado() ? '0' : '1');
      atualizarBotao();
      if (ligado()) {
        tocar(); // teste do som (e libera o áudio no navegador)
        if ('Notification' in window && window.isSecureContext && Notification.permission === 'default') {
          try { Notification.requestPermission(); } catch (e) { }
        }
      }
    });
  }
  atualizarBotao();
  // os navegadores só liberam som depois de um clique na página
  document.addEventListener('pointerdown', function () { if (ligado()) audio(); }, { once: true });

  function consultar() {
    var conhecido = parseInt(ler(ULTIMO) || '0', 10) || 0;
    if (conhecido > ultimo) ultimo = conhecido; // outra aba já avisou
    fetch('/api/chamados/novidades?desde=' + ultimo, { credentials: 'same-origin', cache: 'no-store' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (d) {
        if (!d) return;
        if (ultimo === 0) { ultimo = d.ultimo; gravar(ULTIMO, String(ultimo)); return; } // 1ª vez: só registra
        if (d.ultimo > ultimo) {
          ultimo = d.ultimo; gravar(ULTIMO, String(ultimo));
          if (ligado() && d.novos && d.novos.length) {
            d.novos.forEach(mostrar);
            tocar();
            if (document.hidden) marcarTitulo(d.novos.length);
          }
        }
        var b = document.querySelector('.abas a[href="/chamados"] b');
        if (b && typeof d.abertos === 'number') b.textContent = d.abertos;
      })
      .catch(function () { });
  }
  consultar();
  setInterval(consultar, INTERVALO);
})();
