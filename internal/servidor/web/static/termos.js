// Harvest 1.23 — Termos: unidade conforme a empresa escolhida e máscara do CPF.
(function () {
  function filtrarUnidades(form) {
    var emp = form.querySelector('[data-empresa]');
    var uni = form.querySelector('[data-unidade]');
    if (!emp || !uni) return;
    var tem = false;
    Array.prototype.forEach.call(uni.options, function (o) {
      if (!o.dataset.de) return;
      var mostra = o.dataset.de === emp.value;
      o.hidden = !mostra; o.disabled = !mostra;
      if (mostra) tem = true;
      if (!mostra && o.selected) uni.value = '';
    });
    uni.required = tem;
    uni.closest('label').style.display = tem ? '' : 'none';
  }
  document.querySelectorAll('[data-empresa]').forEach(function (emp) {
    var form = emp.form;
    if (!form || form.dataset.termosOk) return;
    form.dataset.termosOk = '1';
    emp.addEventListener('change', function () { filtrarUnidades(form); });
    filtrarUnidades(form);
  });
  document.querySelectorAll('[data-mascara="cpf"]').forEach(function (c) {
    c.addEventListener('input', function () {
      var d = c.value.replace(/\D/g, '').slice(0, 11), r = d;
      if (d.length > 9) r = d.slice(0, 3) + '.' + d.slice(3, 6) + '.' + d.slice(6, 9) + '-' + d.slice(9);
      else if (d.length > 6) r = d.slice(0, 3) + '.' + d.slice(3, 6) + '.' + d.slice(6);
      else if (d.length > 3) r = d.slice(0, 3) + '.' + d.slice(3);
      c.value = r;
    });
  });
})();

// Emitir termo: o equipamento só aparece para termos por equipamento.
(function () {
  var sel = document.querySelector('[data-tipo-termo]');
  var campo = document.querySelector('[data-campo-equipamento]');
  if (!sel || !campo) return;
  function atualizar() {
    var o = sel.options[sel.selectedIndex];
    var eq = o && o.dataset.equipamento === '1';
    campo.style.display = eq || !sel.value ? '' : 'none';
    campo.querySelector('select').required = eq;
  }
  sel.addEventListener('change', atualizar);
  atualizar();
})();
