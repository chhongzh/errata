$(function () {
  var DATA = window.ERRATA || { problems: [] };
  var P = DATA.problems;
  var state = { subject: '', type: '', tags: [], q: '' };
  var shown = [];
  var modal = new bootstrap.Modal(document.getElementById('detail'));

  var MODES = ['auto', 'light', 'dark'];
  var LABELS = { auto: '自动', light: '浅色', dark: '深色' };
  function applyTheme() {
    var m = localStorage.getItem('errata-theme') || 'auto';
    var dark = m === 'dark' || (m === 'auto' && matchMedia('(prefers-color-scheme: dark)').matches);
    $('html').attr('data-bs-theme', dark ? 'dark' : 'light');
    $('#theme').text(LABELS[m]);
  }
  $('#theme').on('click', function () {
    var m = localStorage.getItem('errata-theme') || 'auto';
    localStorage.setItem('errata-theme', MODES[(MODES.indexOf(m) + 1) % MODES.length]);
    applyTheme();
  });
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', applyTheme);
  applyTheme();

  var NAMES = { subject: {}, type: {} };
  (DATA.subjects || []).forEach(function (x) { NAMES.subject[x.id] = x.name; });
  (DATA.types || []).forEach(function (x) { NAMES.type[x.id] = x.name; });
  function nameOf(kind, id) { return NAMES[kind][id] || id; }
  function count(key) {
    var m = {};
    P.forEach(function (p) {
      (Array.isArray(p[key]) ? p[key] : [p[key]]).forEach(function (v) { m[v] = (m[v] || 0) + 1; });
    });
    return m;
  }
  function typeset(el) {
    if (window.renderMathInElement) {
      renderMathInElement(el, {
        delimiters: [
          { left: '$$', right: '$$', display: true },
          { left: '\\[', right: '\\]', display: true },
   { left: '\\(', right: '\\)', display: false },
   { left: '$', right: '$', display: false }
        ],
        throwOnError: false
      });
    }
  }
  function decorate($root) {
    $root.find('img').addClass('img-fluid');
    $root.find('table').addClass('table table-bordered w-auto');
    $root.find('blockquote').addClass('blockquote border-start ps-3');
    $root.find('pre').addClass('border rounded p-2');
  }
  function hay(p) {
    return [p.id, p.title, p.tags.join(' '), p.subject, nameOf('subject', p.subject), p.type, nameOf('type', p.type), p.source, p.stem_text].join(' ').toLowerCase();
  }
  P.forEach(function (p) { p._hay = hay(p); });

  function item(label, n, active, attrs) {
    return $('<button type="button" class="list-group-item list-group-item-action d-flex justify-content-between align-items-center">')
      .toggleClass('active', active).attr(attrs)
      .append($('<span>').text(label), $('<span class="badge text-bg-secondary rounded-pill">').text(n));
  }
  function renderFilters() {
    var sc = count('subject'), tc = count('type'), gc = count('tags');
    var $s = $('#f-subject').empty(), $t = $('#f-type').empty(), $g = $('#f-tag').empty();
    (DATA.subjects || []).forEach(function (s) {
      if (sc[s.id]) $s.append(item(s.name, sc[s.id], state.subject === s.id, { 'data-subject': s.id }));
    });
    (DATA.types || []).forEach(function (t) {
      if (tc[t.id]) $t.append(item(t.name, tc[t.id], state.type === t.id, { 'data-type': t.id }));
    });
    Object.keys(gc).sort(function (a, b) { return gc[b] - gc[a] || a.localeCompare(b); }).forEach(function (g) {
      var on = state.tags.indexOf(g) >= 0;
      $g.append($('<button type="button" class="btn btn-sm">')
        .addClass(on ? 'btn-primary' : 'btn-outline-secondary')
        .attr('data-tag', g).text(g + ' ' + gc[g]));
    });
  }
  $('#f-subject').on('click', '[data-subject]', function () {
    var v = $(this).data('subject'); state.subject = state.subject === v ? '' : v; refresh();
  });
  $('#f-type').on('click', '[data-type]', function () {
    var v = $(this).data('type'); state.type = state.type === v ? '' : v; refresh();
  });
  $('#f-tag').on('click', '[data-tag]', function () {
    var v = String($(this).data('tag')), i = state.tags.indexOf(v);
    if (i >= 0) state.tags.splice(i, 1); else state.tags.push(v);
    refresh();
  });
  $('#reset').on('click', function () {
    state = { subject: '', type: '', tags: [], q: '' }; $('#q').val(''); refresh();
  });
  $('#q').on('input', function () { state.q = $(this).val().trim().toLowerCase(); refresh(); });

  function match(p) {
    if (state.subject && p.subject !== state.subject) return false;
    if (state.type && p.type !== state.type) return false;
    for (var i = 0; i < state.tags.length; i++) if (p.tags.indexOf(state.tags[i]) < 0) return false;
    if (state.q && p._hay.indexOf(state.q) < 0) return false;
    return true;
  }
  function renderList() {
    shown = P.filter(match);
    var $l = $('#list').empty();
    shown.forEach(function (p) {
      var $tags = $('<span class="d-flex flex-wrap gap-1">');
      p.tags.forEach(function (t) { $tags.append($('<span class="badge text-bg-light border">').text(t)); });
      $l.append($('<a class="list-group-item list-group-item-action d-flex flex-wrap align-items-center gap-2">')
        .attr('href', '#' + p.id).attr('data-id', p.id)
        .append($('<span class="badge text-bg-primary">').text(p.id),
  $('<span class="flex-grow-1">').text(p.title),
         $('<span class="badge text-bg-secondary">').text(nameOf('subject', p.subject)),
         $('<span class="badge text-bg-info">').text(nameOf('type', p.type)),
  $tags));
    });
    $('#count').text('共 ' + shown.length + ' / ' + P.length + ' 题');
    $('#empty').toggleClass('d-none', shown.length > 0);
  }
  function refresh() { renderFilters(); renderList(); }

  var current = null;
  function show(id) {
    var p = P.find(function (x) { return x.id === id; });
    if (!p) return;
    current = p;
    $('#d-title').text(p.id + '  ' + p.title);
    var $m = $('#d-meta').empty()
      .append($('<span class="badge text-bg-secondary">').text(nameOf('subject', p.subject)),
       $('<span class="badge text-bg-info">').text(nameOf('type', p.type)));
    p.tags.forEach(function (t) { $m.append($('<span class="badge text-bg-light border">').text(t)); });
    if (p.difficulty) $m.append($('<span class="badge text-bg-warning">').text('难度 ' + p.difficulty + '/5'));
    $('#d-stem').html(p.stem);
    $('#d-answer').html(p.answer);
    $('#d-solution').html(p.solution);
    $('#d-answer-wrap').toggle(!!p.answer);
    $('#d-solution-wrap').toggle(!!p.solution);
    $('#d-acc .accordion-collapse').removeClass('show');
    $('#d-acc .accordion-button').addClass('collapsed');
    $('#d-source').text([p.source, p.created].filter(Boolean).join(' · '));
    decorate($('#detail .modal-body'));
    typeset(document.getElementById('detail'));
    modal.show();
  }
  function step(d) {
    if (!current || !shown.length) return;
    var i = shown.findIndex(function (x) { return x.id === current.id; });
    show(shown[(i + d + shown.length) % shown.length].id);
    history.replaceState(null, '', '#' + current.id);
  }
  $('#next').on('click', function () { step(1); });
  $('#prev').on('click', function () { step(-1); });
  $('#random').on('click', function () {
    var pool = shown.length ? shown : P;
    if (!pool.length) return;
    var p = pool[Math.floor(Math.random() * pool.length)];
    history.replaceState(null, '', '#' + p.id);
    show(p.id);
  });
  $('#list').on('click', '[data-id]', function (e) {
    e.preventDefault();
    history.replaceState(null, '', '#' + $(this).data('id'));
    show($(this).data('id'));
  });
  document.getElementById('detail').addEventListener('hidden.bs.modal', function () {
    history.replaceState(null, '', location.pathname + location.search);
  });
  $(window).on('hashchange', function () { if (location.hash.length > 1) show(location.hash.slice(1)); });

  refresh();
  if (location.hash.length > 1) show(location.hash.slice(1));
});
