// Harvest (1.18) — easter egg. Código Konami (↑ ↑ ↓ ↓ ← → ← → B A) em qualquer tela:
// um enxame de partículas douradas "colhe" a tela e forma o nome do projeto.
// Projeto Harvest, desenvolvido por Daniel Costa — T.I Ocupacional.
(function () {
  var SEQ = ['ArrowUp', 'ArrowUp', 'ArrowDown', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'ArrowLeft', 'ArrowRight', 'b', 'a'];
  var pos = 0, aberto = false;

  document.addEventListener('keydown', function (e) {
    if (aberto) {
      if (e.key === 'Escape') fechar();
      return;
    }
    var k = e.key.length === 1 ? e.key.toLowerCase() : e.key;
    if (k === SEQ[pos]) {
      pos++;
      if (pos === SEQ.length) {
        pos = 0;
        e.preventDefault(); // o "a" final não chega a ser digitado
        limparCampo(e.target);
        abrir();
      }
    } else {
      pos = k === SEQ[0] ? 1 : 0;
    }
  });

  // se o código foi digitado dentro de um campo (ex.: usuário no login), tira o "b" que entrou
  function limparCampo(el) {
    if (!el || !('value' in el) || typeof el.value !== 'string') return;
    if (/b$/i.test(el.value)) el.value = el.value.slice(0, -1);
    if (el.blur) el.blur();
  }

  var overlay, canvas, ctx, raf, parts = [], mouse = { x: -9999, y: -9999 }, inicio = 0;

  function empresa() {
    var m = document.querySelector('meta[name="harvest-empresa"]');
    return m && m.content ? ' — ' + m.content.replace(/[<>&"]/g, '') : '';
  }

  function versao() {
    var m = document.querySelector('meta[name="harvest-versao"]');
    return m ? m.content : '';
  }

  function abrir() {
    aberto = true;
    var quieto = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    overlay = document.createElement('div');
    overlay.setAttribute('role', 'dialog');
    overlay.setAttribute('aria-label', 'Harvest');
    overlay.style.cssText = 'position:fixed;inset:0;z-index:9999;background:rgba(6,16,18,.94);display:flex;align-items:center;justify-content:center;opacity:0;transition:opacity .35s';
    canvas = document.createElement('canvas');
    canvas.style.cssText = 'position:absolute;inset:0;width:100%;height:100%';
    overlay.appendChild(canvas);

    var cartao = document.createElement('div');
    cartao.style.cssText = 'position:absolute;left:50%;bottom:9vh;transform:translateX(-50%);text-align:center;color:#C9D9D6;font:15px/1.6 Inter,system-ui,sans-serif;opacity:0;transition:opacity .8s 1.6s;max-width:90vw';
    var v = versao();
    cartao.innerHTML =
      '<div style="font:700 13px Inter,sans-serif;letter-spacing:.32em;color:#ADDA23;text-transform:uppercase">Projeto Harvest' + (v ? ' · v' + v.replace(/[^0-9.]/g, '') : '') + '</div>' +
      '<div style="margin-top:6px">Colhendo inventário, chamados e ramais desde 28/10/2026.</div>' +
      '<div style="margin-top:2px">Desenvolvido por <a href="https://www.linkedin.com/in/ccostadaniel/" target="_blank" rel="noopener" style="color:#F2C94C;font-weight:600">Daniel Costa</a>' + empresa() + '</div>' +
      '<div style="margin-top:14px;font-size:12px;color:#6F8A86">Esc ou clique para voltar</div>';
    overlay.appendChild(cartao);
    document.body.appendChild(overlay);

    overlay.addEventListener('click', function (e) { if (e.target.tagName !== 'A') fechar(); });
    overlay.addEventListener('mousemove', function (e) { mouse.x = e.clientX; mouse.y = e.clientY; });
    overlay.addEventListener('mouseleave', function () { mouse.x = mouse.y = -9999; });
    window.addEventListener('resize', montar);

    ctx = canvas.getContext('2d');
    montar();
    requestAnimationFrame(function () { overlay.style.opacity = '1'; cartao.style.opacity = '1'; });
    inicio = performance.now();
    if (quieto) { parts.forEach(function (p) { p.x = p.tx; p.y = p.ty; }); desenhar(); }
    else { raf = requestAnimationFrame(passo); }
  }

  // alvos: os pixels da palavra HARVEST desenhada num canvas fora da tela
  function montar() {
    var dpr = Math.min(window.devicePixelRatio || 1, 2);
    var W = window.innerWidth, H = window.innerHeight;
    canvas.width = W * dpr; canvas.height = H * dpr;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

    var off = document.createElement('canvas');
    off.width = W; off.height = H;
    var o = off.getContext('2d');
    var tam = Math.min(W / 5.2, H / 3.2);
    o.font = '800 ' + tam + 'px Montserrat, Inter, Arial, sans-serif';
    o.textAlign = 'center'; o.textBaseline = 'middle';
    o.fillStyle = '#fff';
    o.fillText('HARVEST', W / 2, H * 0.44);
    var dados = o.getImageData(0, 0, W, H).data;
    var passoPx = Math.max(4, Math.round(tam / 26));
    var alvos = [];
    for (var y = 0; y < H; y += passoPx) {
      for (var x = 0; x < W; x += passoPx) {
        if (dados[(y * W + x) * 4 + 3] > 128) alvos.push([x, y]);
      }
    }
    var cores = ['#ADDA23', '#F2C94C', '#E8A317', '#FFE08A', '#C7EC55'];
    var antigas = parts;
    parts = alvos.map(function (a, i) {
      var p = antigas[i];
      if (!p) {
        // nascem nas bordas da tela, como um enxame chegando
        var lado = Math.random() * 4 | 0;
        p = {
          x: lado === 0 ? -20 : lado === 1 ? W + 20 : Math.random() * W,
          y: lado === 2 ? -20 : lado === 3 ? H + 20 : Math.random() * H,
          vx: 0, vy: 0,
          r: 1.2 + Math.random() * 1.8,
          c: cores[Math.random() * cores.length | 0],
          atraso: Math.random() * 900,
          fase: Math.random() * Math.PI * 2
        };
      }
      p.tx = a[0]; p.ty = a[1];
      return p;
    });
  }

  function passo(t) {
    var dt = t - inicio;
    for (var i = 0; i < parts.length; i++) {
      var p = parts[i];
      if (dt < p.atraso) continue;
      var dx = p.tx - p.x, dy = p.ty - p.y;
      p.vx = (p.vx + dx * 0.012) * 0.86;
      p.vy = (p.vy + dy * 0.012) * 0.86;
      // o mouse espanta o enxame
      var mx = p.x - mouse.x, my = p.y - mouse.y, d2 = mx * mx + my * my;
      if (d2 < 9000) {
        var f = (9000 - d2) / 9000 * 2.6;
        var d = Math.sqrt(d2) || 1;
        p.vx += mx / d * f; p.vy += my / d * f;
      }
      p.x += p.vx; p.y += p.vy;
    }
    desenhar(t);
    raf = requestAnimationFrame(passo);
  }

  function desenhar(t) {
    var W = window.innerWidth, H = window.innerHeight;
    ctx.clearRect(0, 0, W, H);
    for (var i = 0; i < parts.length; i++) {
      var p = parts[i];
      var brilho = t ? 0.75 + 0.25 * Math.sin(t / 380 + p.fase) : 1;
      ctx.globalAlpha = brilho;
      ctx.fillStyle = p.c;
      ctx.beginPath();
      ctx.arc(p.x, p.y, p.r, 0, Math.PI * 2);
      ctx.fill();
    }
    ctx.globalAlpha = 1;
  }

  function fechar() {
    if (!overlay) return;
    cancelAnimationFrame(raf);
    window.removeEventListener('resize', montar);
    var o = overlay;
    overlay = null; parts = []; aberto = false;
    o.style.opacity = '0';
    setTimeout(function () { o.remove(); }, 350);
  }
})();
