// Harvest 1.24 — Pedir equipamento: item do catálogo preenche a especificação sugerida.
(function () {
  var sel = document.querySelector('[data-catalogo]');
  if (!sel) return;
  var livre = document.querySelector('[data-item-livre]');
  var espec = document.querySelector('[data-espec-campo]');
  function atualizar() {
    var o = sel.options[sel.selectedIndex];
    var doCatalogo = !!sel.value;
    livre.style.display = doCatalogo ? 'none' : '';
    livre.querySelector('input').required = !doCatalogo;
    if (doCatalogo && espec && (!espec.value || espec.dataset.auto === '1')) {
      espec.value = o.dataset.espec || '';
      espec.dataset.auto = '1';
    }
  }
  if (espec) espec.addEventListener('input', function () { espec.dataset.auto = ''; });
  sel.addEventListener('change', atualizar);
  atualizar();
})();
