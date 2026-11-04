// Configurações › Marca: sincroniza seletor de cor e campo hex e atualiza a prévia.
(function () {
  var prev = document.getElementById('previa-marca');
  if (!prev) return;
  function lum(hex) {
    var n = parseInt(hex.slice(1), 16), c = [n >> 16 & 255, n >> 8 & 255, n & 255].map(function (v) {
      v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
    });
    return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
  }
  function sobre(hex) { return lum(hex) > 0.179 ? '#111111' : '#FFFFFF'; }
  var ok = /^#[0-9a-fA-F]{6}$/;
  function liga(nome) {
    var sel = document.getElementById('cor-' + nome + '-sel'), txt = document.getElementById('cor-' + nome);
    function aplica(v) {
      if (!ok.test(v)) return;
      prev.style.setProperty('--pm-' + nome, v);
      prev.style.setProperty('--pm-' + nome + '-t', sobre(v));
    }
    sel.addEventListener('input', function () { txt.value = sel.value.toUpperCase(); aplica(sel.value); });
    txt.addEventListener('input', function () {
      var v = txt.value.trim(); if (v && v[0] !== '#') v = '#' + v;
      if (ok.test(v)) { sel.value = v.toLowerCase(); aplica(v); }
    });
    aplica(txt.value || sel.value);
  }
  liga('pri'); liga('sec');
  var nome = document.querySelector('.form-marca input[name=nome]'), pm = document.getElementById('pm-nome');
  if (nome && pm) nome.addEventListener('input', function () { pm.textContent = nome.value.trim() || nome.placeholder; });
  // prévia local da logo escolhida (antes de salvar)
  document.querySelectorAll('.caixa-logo input[type=file]').forEach(function (inp) {
    inp.addEventListener('change', function () {
      var img = inp.parentNode.querySelector('img');
      if (inp.files && inp.files[0] && img) img.src = URL.createObjectURL(inp.files[0]);
    });
  });
})();
