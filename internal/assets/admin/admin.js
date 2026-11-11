$(function () {
  var META = { subjects: [], types: [], tags: {} };
  var LIST = [];
  var current = null;
  var activeTab = 'stem';
  var draft = { stem: '', answer: '', solution: '' };
  var dirtyForm = false;
  var previewTimer = null;
  var addKind = '';

  var TABS = [['stem', '题干'], ['answer', '答案'], ['solution', '解析']];
  var addModal = new bootstrap.Modal('#add-modal');
  var syncModal = new bootstrap.Modal('#sync-modal');
  var toast = new bootstrap.Toast('#toast', { delay: 2500 });

  function api(method, url, body, raw) {
    return $.ajax({
      method: method,
      url: url,
      headers: { 'X-Errata': '1' },
      contentType: raw ? raw.type : 'application/json',
      processData: false,
      data: raw ? raw.data : (body === undefined ? undefined : JSON.stringify(body)),
      dataType: 'json'
    });
  }
  function errText(xhr) {
    var j = xhr.responseJSON;
    if (j && j.error) return [j.error].concat(j.log || []).join('\n');
    return xhr.statusText || '请求失败';
  }
  function say(msg) { $('#toast-body').text(msg); toast.show(); }
  function label(list, id) {
    var f = list.filter(function (x) { return x.id === id; })[0];
    return f ? f.name : id;
  }

  // theme
  var MODES = ['auto', 'light', 'dark'], LABELS = { auto: '自动', light: '浅色', dark: '深色' };
  function applyTheme() {
    var m = localStorage.getItem('errata-theme') || 'auto';
    var dark = m === 'dark' || (m === 'auto' && matchMedia('(prefers-color-scheme: dark)').matches);
    $('html').attr('data-bs-theme', dark ? 'dark' : 'light');
    $('#theme').text(LABELS[m]);
  }
  $('#theme').on('click', function () {
    var m = localStorage.getItem('errata-theme') || 'auto';
    localStorage.setItem('errata-theme', MODES[(MODES.indexOf(m) + 1) % 3]);
    applyTheme();
  });
  applyTheme();

  // data
  function loadMeta() {
    return api('GET', '/api/meta').then(function (m) {
      META = m;
      $('#brand').text(m.title + ' · editor');
      document.title = m.title + ' editor';
      fillSelect('#f-subject', m.subjects, '全部科目');
      fillSelect('#f-type', m.types, '全部题型');
      fillSelect('#e-subject', m.subjects);
      fillSelect('#e-type', m.types);
      $('#tag-options').empty().append(Object.keys(m.tags).sort().map(function (t) {
        return $('<option>').attr('value', t);
      }));
    });
  }
  function fillSelect(sel, list, all) {
    var $s = $(sel), v = $s.val();
    $s.empty();
    if (all) $s.append($('<option>').attr('value', '').text(all));
    list.forEach(function (n) { $s.append($('<option>').attr('value', n.id).attr('title', n.id).text(n.name)); });
    if (v) $s.val(v);
  }
  function loadList() {
    return api('GET', '/api/problems').then(function (l) { LIST = l; renderList(); });
  }
  function loadStatus() {
    api('GET', '/api/status').then(function (s) {
      var n = (s.dirty || []).length;
      $('#dirty').text(n ? n + ' 个未同步变更' : '已同步');
    });
  }

  // list
  function renderList() {
    var q = $('#q').val().trim().toLowerCase(), fs = $('#f-subject').val(), ft = $('#f-type').val();
    var rows = LIST.filter(function (p) {
      if (fs && p.subject !== fs) return false;
      if (ft && p.type !== ft) return false;
      return !q || (p.id + ' ' + p.title + ' ' + p.tags.join(' ')).toLowerCase().indexOf(q) >= 0;
    });
    var $l = $('#list').empty();
    rows.forEach(function (p) {
      $l.append($('<button type="button" class="list-group-item list-group-item-action">')
        .toggleClass('active', current && current.id === p.id)
        .attr('data-id', p.id)
        .append($('<div class="d-flex gap-2">').append(
   $('<span class="badge text-bg-primary">').text(p.id),
          $('<span class="flex-grow-1 text-truncate">').text(p.title)),
   $('<div class="small opacity-75">').text(label(META.subjects, p.subject) + ' · ' + label(META.types, p.type) + ' · ' + p.tags.join(', '))));
    });
    $('#count').text(rows.length + ' / ' + LIST.length + ' 题');
    $('#list-empty').toggleClass('d-none', LIST.length > 0);
  }
  $('#q').on('input', renderList);
  $('#f-subject, #f-type').on('change', renderList);
  $('#list').on('click', '[data-id]', function () {
    var id = $(this).data('id');
    guardDirty(function () { openProblem(id); });
  });

  function guardDirty(next) {
    if (dirtyForm && !confirm('当前改动尚未保存，放弃并继续？')) return;
    next();
  }

  // editor
  function showEditor(on) {
    $('#editor').toggleClass('d-none', !on);
    $('#welcome').toggleClass('d-none', on);
  }
  function setForm(d, isNew) {
    current = isNew ? { id: '' } : { id: d.id };
    $('#e-heading').text(isNew ? '新建题目' : d.id);
    $('#e-delete').toggleClass('d-none', isNew);
    $('#e-title').val(d.title || '');
    $('#e-subject').val(d.subject || (META.subjects[0] || {}).id);
    $('#e-type').val(d.type || (META.types[0] || {}).id);
    $('#e-tags').val((d.tags || []).join(', '));
    $('#e-source').val(d.source || '');
    $('#e-difficulty').val(String(d.difficulty || 0));
    draft = { stem: d.stem || '', answer: d.answer || '', solution: d.solution || '' };
    $('#e-errors').addClass('d-none');
    showTab('stem');
    dirtyForm = false;
    showEditor(true);
    renderList();
    checkTags();
  }
  function renderTabs() {
    $('#e-tabs').empty().append(TABS.map(function (t) {
      var filled = draft[t[0]].trim() ? '' : ' text-body-secondary';
      return $('<li class="nav-item">').append(
        $('<button type="button" class="nav-link' + filled + '">').toggleClass('active', t[0] === activeTab)
          .attr('data-tab', t[0]).text(t[1]));
    }));
  }
  function showTab(k) {
    activeTab = k;
    $('#e-text').val(draft[k]);
    renderTabs();
    preview();
  }
  function switchTab(k) {
    draft[activeTab] = $('#e-text').val();
    showTab(k);
  }
  $('#e-tabs').on('click', '[data-tab]', function () { switchTab($(this).data('tab')); });

  function openProblem(id) {
    api('GET', '/api/problems/' + id).then(function (d) { setForm(d, false); },
      function (x) { say(errText(x)); });
  }
  $('#new').on('click', function () {
    guardDirty(function () {
      setForm({ subject: $('#f-subject').val() || '', type: $('#f-type').val() || '' }, true);
      $('#e-title').trigger('focus');
    });
  });

  function preview() {
    var text = $('#e-text').val();
    clearTimeout(previewTimer);
    previewTimer = setTimeout(function () {
      if (!text.trim()) { $('#e-preview').html('<span class="text-body-secondary">预览</span>'); return; }
      api('POST', '/api/preview', { text: text }).then(function (r) {
 $('#e-preview').html(r.html);
        $('#e-preview').find('img').addClass('img-fluid');
        $('#e-preview').find('table').addClass('table table-bordered w-auto');
        if (window.renderMathInElement) {
   renderMathInElement(document.getElementById('e-preview'), {
     delimiters: [
       { left: '$$', right: '$$', display: true }, { left: '\\[', right: '\\]', display: true },
       { left: '\\(', right: '\\)', display: false }, { left: '$', right: '$', display: false }
     ], throwOnError: false
   });
 }
      });
    }, 250);
  }
  $('#e-text').on('input', function () { dirtyForm = true; draft[activeTab] = $(this).val(); renderTabs(); preview(); });
  $('#editor').on('input change', 'input, select', function () { dirtyForm = true; });

  function parseTags(s) {
    return s.split(/[,，]/).map(function (t) { return t.trim(); }).filter(Boolean);
  }
  function checkTags() {
    var bad = parseTags($('#e-tags').val()).filter(function (t) {
      return !/^[a-z0-9]+(-[a-z0-9]+)*$/.test(t.toLowerCase().replace(/[ _]+/g, '-'));
    });
    $('#e-tag-hint').text(bad.length ? '不是英文 kebab-case: ' + bad.join(', ') : '').toggleClass('text-danger', bad.length > 0);
  }
  $('#e-tags').on('input', checkTags);

  function insertAtCursor(text) {
    var el = document.getElementById('e-text'), a = el.selectionStart, b = el.selectionEnd;
    el.value = el.value.slice(0, a) + text + el.value.slice(b);
    el.selectionStart = el.selectionEnd = a + text.length;
    $(el).trigger('input').trigger('focus');
  }
  function uploadImage(file) {
    if (!file) return;
    var rd = new FileReader();
    rd.onload = function () {
      api('POST', '/api/upload', undefined, { type: file.type, data: rd.result }).then(function (r) {
 insertAtCursor('![](' + r.path + ')');
      }, function (x) { say(errText(x)); });
    };
    rd.readAsArrayBuffer(file);
  }
  $('#e-image').on('change', function () { uploadImage(this.files[0]); this.value = ''; });
  $('#e-text').on('paste', function (e) {
    var items = (e.originalEvent.clipboardData || {}).items || [];
    for (var i = 0; i < items.length; i++) {
      if (items[i].type.indexOf('image/') === 0) { e.preventDefault(); uploadImage(items[i].getAsFile()); return; }
    }
  });

  $('#editor').on('submit', function (e) {
    e.preventDefault();
    draft[activeTab] = $('#e-text').val();
    var doc = {
      title: $('#e-title').val().trim(), subject: $('#e-subject').val(), type: $('#e-type').val(),
      tags: parseTags($('#e-tags').val()), source: $('#e-source').val().trim(),
      difficulty: parseInt($('#e-difficulty').val(), 10) || 0,
      stem: draft.stem, answer: draft.answer, solution: draft.solution
    };
    var isNew = !current.id;
    api(isNew ? 'POST' : 'PUT', isNew ? '/api/problems' : '/api/problems/' + current.id, doc).then(function (saved) {
      say('已保存 ' + saved.id);
      return $.when(loadMeta(), loadList()).then(function () { setForm(saved, false); loadStatus(); });
    }, function (x) {
      var j = x.responseJSON || {};
      $('#e-errors').removeClass('d-none').empty().append(
        $('<div>').text(j.error || '保存失败'),
 $('<ul class="mb-0">').append((j.log || []).map(function (l) { return $('<li>').text(l); })));
      $('#e-errors')[0].scrollIntoView({ block: 'nearest' });
    });
  });

  $('#e-delete').on('click', function () {
    if (!current.id || !confirm('删除 ' + current.id + '？此操作会写入仓库，可用 Git 找回。')) return;
    api('DELETE', '/api/problems/' + current.id).then(function () {
      say('已删除 ' + current.id);
      current = null; dirtyForm = false; showEditor(false);
      $.when(loadMeta(), loadList()).then(loadStatus);
    }, function (x) { say(errText(x)); });
  });

  // add subject/type
  $('#editor').on('click', '[data-add]', function () {
    addKind = $(this).data('add');
    $('#add-title').text(addKind === 'subject' ? '新增科目' : '新增题型');
    $('#add-id, #add-name').val('');
    $('#add-error').addClass('d-none');
    addModal.show();
  });
  document.getElementById('add-modal').addEventListener('shown.bs.modal', function () { $('#add-id').trigger('focus'); });
  $('#add-form').on('submit', function (e) {
    e.preventDefault();
    var id = $('#add-id').val().trim(), name = $('#add-name').val().trim();
    api('POST', '/api/' + addKind + 's', { id: id, name: name }).then(function () {
      addModal.hide();
      return loadMeta().then(function () {
        $(addKind === 'subject' ? '#e-subject' : '#e-type').val(id);
        dirtyForm = true;
      });
    }, function (x) { $('#add-error').removeClass('d-none').text(errText(x)); });
  });

  // sync
  $('#sync').on('click', function () {
    var $b = $(this).prop('disabled', true).text('同步中');
    $('#sync-log').text('');
    api('POST', '/api/sync').then(function (r) {
      $('#sync-log').text((r.log || []).join('\n') || '完成');
    }, function (x) { $('#sync-log').text(errText(x)); }).always(function () {
      $b.prop('disabled', false).text('同步');
      syncModal.show();
      loadStatus();
    });
  });

  window.addEventListener('beforeunload', function (e) { if (dirtyForm) { e.preventDefault(); } });

  $.when(loadMeta().then(loadList), loadStatus()).then(function () {
    var h = location.hash.slice(1);
    if (h) openProblem(h);
  });
});
