// Harvest 1.23 — Administração em seções: mostra um grupo por vez (sem JS, mostra tudo).
(function () {
  var corpo = document.getElementById('admin-corpo');
  if (!corpo) return;
  var secoes = corpo.querySelectorAll('[data-grupo]');
  var links = document.querySelectorAll('.admin-secoes a[data-alvo]');
  function grupoDe(hash) {
    var id = (hash || '').replace(/^#/, '');
    if (!id) return 'geral';
    var el = document.getElementById(id);
    if (el) {
      var s = el.closest('[data-grupo]');
      if (s) return s.getAttribute('data-grupo');
    }
    for (var i = 0; i < secoes.length; i++) if (secoes[i].getAttribute('data-grupo') === id) return id;
    return 'geral';
  }
  function mostrar() {
    var g = grupoDe(location.hash);
    secoes.forEach(function (s) { s.hidden = s.getAttribute('data-grupo') !== g; });
    links.forEach(function (a) { a.classList.toggle('ativo', a.getAttribute('data-alvo') === g); });
    var id = location.hash.replace(/^#/, '');
    var el = id && document.getElementById(id);
    if (el && el.getAttribute('data-grupo') == null) el.scrollIntoView({ block: 'start' });
    else window.scrollTo(0, 0);
  }
  window.addEventListener('hashchange', mostrar);
  mostrar();
})();
