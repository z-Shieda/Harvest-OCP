// Anexos nos formulários de chamado: lista os arquivos escolhidos, permite remover,
// colar prints (Ctrl+V) e arrastar arquivos. Confere quantidade e tamanho antes de enviar.
(function () {
  var BLOQ = /\.(exe|msi|bat|cmd|com|scr|pif|ps1|psm1|vbs|vbe|js|jse|wsf|wsh|hta|lnk|dll|jar|reg|cpl|msp|appx|msix|iso|img|vhdx?|html?|svg)$/i;

  function tam(b) {
    if (b < 1024) return b + ' B';
    if (b < 1048576) return (b / 1024).toFixed(0) + ' KB';
    return (b / 1048576).toFixed(1) + ' MB';
  }

  document.querySelectorAll('form[data-anexos]').forEach(function (form) {
    var input = form.querySelector('input[type=file][name=anexos]');
    if (!input || typeof DataTransfer === 'undefined') return;
    var zona = form.querySelector('.zona-anexos');
    var lista = form.querySelector('.lista-arquivos');
    var maxArq = parseInt(form.dataset.maxArquivos || '5', 10);
    var maxMB = parseInt(form.dataset.maxMb || '10', 10);
    var arquivos = [];

    function sincronizar() {
      var dt = new DataTransfer();
      arquivos.forEach(function (f) { dt.items.add(f); });
      input.files = dt.files;
      desenhar();
    }

    function problemas() {
      var p = [];
      if (arquivos.length > maxArq) p.push('Máximo de ' + maxArq + ' arquivos por envio.');
      arquivos.forEach(function (f) {
        if (f.size > maxMB * 1048576) p.push(f.name + ' passa de ' + maxMB + ' MB.');
        if (BLOQ.test(f.name)) p.push(f.name + ': tipo de arquivo não permitido.');
      });
      return p;
    }

    function desenhar() {
      if (!lista) return;
      lista.innerHTML = '';
      arquivos.forEach(function (f, i) {
        var d = document.createElement('div');
        var n = document.createElement('span');
        n.textContent = '📎 ' + f.name + ' (' + tam(f.size) + ')';
        var b = document.createElement('button');
        b.type = 'button'; b.className = 'btn link'; b.textContent = 'remover';
        b.onclick = function () { arquivos.splice(i, 1); sincronizar(); };
        d.appendChild(n); d.appendChild(b);
        lista.appendChild(d);
      });
      problemas().forEach(function (t) {
        var e = document.createElement('div'); e.className = 'erro'; e.textContent = '⚠ ' + t; lista.appendChild(e);
      });
    }

    function adicionar(files) {
      for (var i = 0; i < files.length; i++) {
        var f = files[i];
        if (!f.name || f.name === 'image.png') {
          // print colado: dá um nome com data e hora
          var d = new Date(), p = function (x) { return ('0' + x).slice(-2); };
          var ext = (f.type.split('/')[1] || 'png').replace('jpeg', 'jpg');
          f = new File([f], 'print-' + d.getFullYear() + p(d.getMonth() + 1) + p(d.getDate()) + '-' + p(d.getHours()) + p(d.getMinutes()) + p(d.getSeconds()) + (i ? '-' + i : '') + '.' + ext, { type: f.type });
        }
        arquivos.push(f);
      }
      sincronizar();
    }

    input.addEventListener('change', function () {
      var novos = Array.prototype.slice.call(input.files);
      adicionar(novos);
    });
    // Ao escolher arquivos, o input é substituído pela lista acumulada: evita perder os anteriores.
    input.addEventListener('click', function () { input.value = ''; });

    form.addEventListener('paste', function (e) {
      var fs = e.clipboardData && e.clipboardData.files;
      if (fs && fs.length) { e.preventDefault(); adicionar(fs); }
    });
    if (zona) {
      ['dragenter', 'dragover'].forEach(function (ev) {
        zona.addEventListener(ev, function (e) { e.preventDefault(); zona.classList.add('arrastando'); });
      });
      ['dragleave', 'drop'].forEach(function (ev) {
        zona.addEventListener(ev, function (e) { e.preventDefault(); zona.classList.remove('arrastando'); });
      });
      zona.addEventListener('drop', function (e) { if (e.dataTransfer.files.length) adicionar(e.dataTransfer.files); });
    }
    form.addEventListener('submit', function (e) {
      // garante que o campo leva todos os arquivos acumulados (mesmo se o seletor foi cancelado)
      var dt = new DataTransfer();
      arquivos.forEach(function (f) { dt.items.add(f); });
      input.files = dt.files;
      if (problemas().length) { e.preventDefault(); desenhar(); return; }
      var btn = form.querySelector('button[type=submit],button:not([type])');
      if (btn) { setTimeout(function () { btn.disabled = true; btn.textContent = 'Enviando…'; }, 0); }
    });
  });

  // botão que copia um texto fixo (ex.: link público de chamados no Início)
  document.querySelectorAll('[data-copiar-texto]').forEach(function (b) {
    b.addEventListener('click', function () {
      var txt = b.dataset.copiarTexto, alvo = b.querySelector('span') || b;
      var ok = function () { var t = alvo.textContent; alvo.textContent = 'Link copiado!'; setTimeout(function () { alvo.textContent = t; }, 1600); };
      if (navigator.clipboard && window.isSecureContext) { navigator.clipboard.writeText(txt).then(ok, function () { prompt('Copie o link:', txt); }); }
      else { var i = document.createElement('textarea'); i.value = txt; document.body.appendChild(i); i.select(); try { document.execCommand('copy'); ok(); } catch (e) { prompt('Copie o link:', txt); } document.body.removeChild(i); }
    });
  });

  // botão "copiar" dos links
  document.querySelectorAll('[data-copiar]').forEach(function (b) {
    b.addEventListener('click', function () {
      var alvo = document.getElementById(b.dataset.copiar);
      if (!alvo) return;
      alvo.select();
      try { document.execCommand('copy'); } catch (x) { }
      try { if (navigator.clipboard && window.isSecureContext) navigator.clipboard.writeText(alvo.value); } catch (x) { }
      var t = b.textContent; b.textContent = 'Copiado!'; setTimeout(function () { b.textContent = t; }, 1500);
    });
  });
})();
