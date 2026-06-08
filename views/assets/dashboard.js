document.addEventListener('DOMContentLoaded', function () {
  document.querySelectorAll('[data-test-webhook]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var domainID = btn.getAttribute('data-test-webhook');
      var resultEl = document.getElementById('webhook-result-' + domainID);
      if (!resultEl) return;

      btn.disabled = true;
      btn.textContent = 'Gönderiliyor…';
      resultEl.className = 'mt-2 rounded-2xl border p-3 text-xs font-semibold';
      resultEl.textContent = '';

      fetch('/merchant/domains/' + domainID + '/test-webhook', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
      })
        .then(function (r) { return r.json(); })
        .then(function (data) {
          resultEl.classList.remove('hidden');
          if (data.success) {
            resultEl.classList.add('border-green-200', 'bg-green-50', 'text-green-800');
            resultEl.textContent = '✓ HTTP ' + data.status_code + ' — webhook başarıyla iletildi.';
          } else {
            resultEl.classList.add('border-red-200', 'bg-red-50', 'text-red-800');
            resultEl.textContent = '✗ ' + (data.error || ('HTTP ' + data.status_code));
            if (data.response) resultEl.textContent += ' — ' + data.response.slice(0, 200);
          }
        })
        .catch(function (err) {
          resultEl.classList.remove('hidden');
          resultEl.classList.add('border-red-200', 'bg-red-50', 'text-red-800');
          resultEl.textContent = '✗ İstek başarısız: ' + err.message;
        })
        .finally(function () {
          btn.disabled = false;
          btn.textContent = 'Webhook Test Et';
        });
    });
  });


  document.querySelectorAll('[data-copy-target], [data-copy-value]').forEach(function (button) {
    button.addEventListener('click', function () {
      var value = button.getAttribute('data-copy-value') || '';
      var target = document.getElementById(button.getAttribute('data-copy-target'));
      if (!value && target) {
        value = target.innerText || target.value || '';
      }
      if (!value) {
        return;
      }
      if (navigator.clipboard) {
        navigator.clipboard.writeText(value);
      }
      var originalText = button.getAttribute('data-copy-original-text');
      if (originalText === null) {
        originalText = button.innerText;
        button.setAttribute('data-copy-original-text', originalText);
      }
      button.innerText = 'Kopyalandı';
      window.setTimeout(function () {
        button.innerText = originalText;
      }, 1200);
    });
  });

  initAdminRichSelects();

  document.querySelectorAll('[data-generate-secret]').forEach(function (button) {
    button.addEventListener('click', function () {
      var input = document.getElementById(button.getAttribute('data-generate-secret'));
      if (!input) {
        return;
      }
      var bytes = new Uint8Array(32);
      if (window.crypto && window.crypto.getRandomValues) {
        window.crypto.getRandomValues(bytes);
      } else {
        for (var i = 0; i < bytes.length; i += 1) {
          bytes[i] = Math.floor(Math.random() * 256);
        }
      }
      input.value = Array.prototype.map.call(bytes, function (byte) {
        return byte.toString(16).padStart(2, '0');
      }).join('');
      input.focus();
    });
  });
});

function initAdminRichSelects() {
  var controls = [];

  document.querySelectorAll('select[data-rich-select]').forEach(function (select) {
    if (select.getAttribute('data-rich-select-ready') === 'true') return;
    select.setAttribute('data-rich-select-ready', 'true');

    var kind = select.getAttribute('data-rich-select') || 'default';
    var placeholder = select.getAttribute('data-placeholder') || 'Seç';
    var root = document.createElement('div');
    var trigger = document.createElement('button');
    var avatar = document.createElement('span');
    var copy = document.createElement('span');
    var primary = document.createElement('span');
    var meta = document.createElement('span');
    var chip = document.createElement('span');
    var caret = document.createElement('span');
    var menu = document.createElement('div');
    var searchWrap = document.createElement('div');
    var search = document.createElement('input');
    var optionsEl = document.createElement('div');

    root.className = 'admin-rich-select';
    root.setAttribute('data-kind', kind);
    root.setAttribute('data-open', 'false');
    root.setAttribute('data-empty', 'true');

    trigger.type = 'button';
    trigger.className = 'admin-rich-trigger';
    trigger.setAttribute('aria-haspopup', 'listbox');
    trigger.setAttribute('aria-expanded', 'false');

    avatar.className = 'admin-rich-avatar';
    copy.className = 'admin-rich-copy';
    primary.className = 'admin-rich-primary';
    meta.className = 'admin-rich-meta';
    chip.className = 'admin-rich-chip';
    caret.className = 'admin-rich-caret';
    caret.setAttribute('aria-hidden', 'true');

    menu.className = 'admin-rich-menu';
    menu.hidden = true;

    searchWrap.className = 'admin-rich-search-wrap';
    search.className = 'admin-rich-search';
    search.type = 'search';
    search.autocomplete = 'off';
    search.spellcheck = false;
    search.placeholder = 'Ara';

    optionsEl.className = 'admin-rich-options';
    optionsEl.setAttribute('role', 'listbox');

    copy.appendChild(primary);
    copy.appendChild(meta);
    trigger.appendChild(avatar);
    trigger.appendChild(copy);
    trigger.appendChild(chip);
    trigger.appendChild(caret);
    searchWrap.appendChild(search);
    menu.appendChild(searchWrap);
    menu.appendChild(optionsEl);
    root.appendChild(trigger);
    root.appendChild(menu);

    select.classList.add('admin-native-select-hidden');
    select.setAttribute('tabindex', '-1');
    select.parentNode.insertBefore(root, select.nextSibling);

    function optionRecords() {
      return Array.prototype.slice.call(select.options).filter(function (option) {
        return option.value !== '';
      }).map(function (option) {
        return readOption(option);
      });
    }

    function selectedRecord() {
      var option = select.options[select.selectedIndex];
      if (!option || !option.value) return null;
      return readOption(option);
    }

    function updateTrigger() {
      var record = selectedRecord();
      var empty = !record;
      root.setAttribute('data-empty', empty ? 'true' : 'false');
      avatar.textContent = empty ? initials(placeholder) : record.avatar;
      primary.textContent = empty ? placeholder : record.primary;
      meta.textContent = empty ? placeholder : record.meta;
      chip.textContent = empty ? '' : record.chip;
      chip.hidden = empty || !record.chip;
    }

    function renderOptions(query) {
      var normalizedQuery = normalize(query);
      var records = optionRecords().filter(function (record) {
        return !normalizedQuery || record.search.indexOf(normalizedQuery) !== -1;
      });
      optionsEl.textContent = '';

      if (records.length === 0) {
        var empty = document.createElement('div');
        empty.className = 'admin-rich-empty';
        empty.textContent = 'Sonuç yok';
        optionsEl.appendChild(empty);
        return;
      }

      records.forEach(function (record) {
        var optionButton = document.createElement('button');
        optionButton.type = 'button';
        optionButton.className = 'admin-rich-option';
        optionButton.setAttribute('role', 'option');
        optionButton.setAttribute('aria-selected', record.value === select.value ? 'true' : 'false');
        optionButton.setAttribute('data-value', record.value);

        var optionAvatar = document.createElement('span');
        var optionCopy = document.createElement('span');
        var optionPrimary = document.createElement('span');
        var optionMeta = document.createElement('span');
        var optionChip = document.createElement('span');
        var optionCheck = document.createElement('span');

        optionAvatar.className = 'admin-rich-avatar';
        optionAvatar.textContent = record.avatar;
        optionCopy.className = 'admin-rich-copy';
        optionPrimary.className = 'admin-rich-primary';
        optionPrimary.textContent = record.primary;
        optionMeta.className = 'admin-rich-meta';
        optionMeta.textContent = record.meta;
        optionChip.className = 'admin-rich-chip';
        optionChip.textContent = record.chip;
        optionChip.hidden = !record.chip;
        optionCheck.className = 'admin-rich-check';
        optionCheck.setAttribute('aria-hidden', 'true');

        optionCopy.appendChild(optionPrimary);
        optionCopy.appendChild(optionMeta);
        optionButton.appendChild(optionAvatar);
        optionButton.appendChild(optionCopy);
        optionButton.appendChild(optionChip);
        optionButton.appendChild(optionCheck);

        optionButton.addEventListener('click', function () {
          choose(record.value);
        });
        optionButton.addEventListener('keydown', function (event) {
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault();
            choose(record.value);
            return;
          }
          if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
            event.preventDefault();
            focusSiblingOption(optionButton, event.key === 'ArrowDown' ? 1 : -1);
            return;
          }
          if (event.key === 'Escape') {
            event.preventDefault();
            closeMenu();
            trigger.focus();
          }
        });

        optionsEl.appendChild(optionButton);
      });
    }

    function choose(value) {
      select.value = value;
      root.setAttribute('data-invalid', 'false');
      select.dispatchEvent(new Event('change', { bubbles: true }));
      updateTrigger();
      renderOptions(search.value);
      closeMenu();
      trigger.focus();
    }

    function openMenu() {
      controls.forEach(function (control) {
        if (control.root !== root) control.close();
      });
      root.setAttribute('data-open', 'true');
      trigger.setAttribute('aria-expanded', 'true');
      menu.hidden = false;
      search.value = '';
      renderOptions('');
      window.setTimeout(function () {
        search.focus();
        var selected = optionsEl.querySelector('[aria-selected="true"]');
        if (selected) selected.scrollIntoView({ block: 'nearest' });
      }, 0);
    }

    function closeMenu() {
      root.setAttribute('data-open', 'false');
      trigger.setAttribute('aria-expanded', 'false');
      menu.hidden = true;
    }

    function toggleMenu() {
      if (menu.hidden) openMenu();
      else closeMenu();
    }

    trigger.addEventListener('click', function (event) {
      event.preventDefault();
      toggleMenu();
    });

    trigger.addEventListener('keydown', function (event) {
      if (event.key === 'Enter' || event.key === ' ' || event.key === 'ArrowDown') {
        event.preventDefault();
        openMenu();
      }
    });

    search.addEventListener('input', function () {
      renderOptions(search.value);
    });

    search.addEventListener('keydown', function (event) {
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        focusFirstOption();
        return;
      }
      if (event.key === 'Escape') {
        event.preventDefault();
        closeMenu();
        trigger.focus();
      }
    });

    select.addEventListener('change', function () {
      root.setAttribute('data-invalid', 'false');
      updateTrigger();
      renderOptions(search.value);
    });

    select.addEventListener('invalid', function (event) {
      event.preventDefault();
      root.setAttribute('data-invalid', 'true');
      openMenu();
      trigger.focus();
    });

    controls.push({ root: root, close: closeMenu });
    updateTrigger();
  });

  document.addEventListener('click', function (event) {
    controls.forEach(function (control) {
      if (!control.root.contains(event.target)) control.close();
    });
  });

  document.addEventListener('keydown', function (event) {
    if (event.key !== 'Escape') return;
    controls.forEach(function (control) {
      control.close();
    });
  });
}

function readOption(option) {
  var text = compactText(option.textContent || '');
  var primary = option.getAttribute('data-primary') || text;
  var meta = option.getAttribute('data-meta') || text;
  var chip = option.getAttribute('data-chip') || '';
  var avatarSource = option.getAttribute('data-avatar') || primary;
  var search = [
    option.getAttribute('data-search') || '',
    option.value,
    text,
    primary,
    meta,
    chip,
  ].join(' ');

  return {
    value: option.value,
    primary: compactText(primary),
    meta: compactText(meta),
    chip: compactText(chip),
    avatar: initials(avatarSource),
    search: normalize(search),
  };
}

function compactText(value) {
  return String(value || '').replace(/\s+/g, ' ').trim();
}

function normalize(value) {
  return compactText(value).toLocaleLowerCase('tr-TR');
}

function initials(value) {
  var words = compactText(value).split(/[\s._/-]+/).filter(Boolean);
  if (words.length === 0) return '?';
  if (words.length === 1) return words[0].slice(0, 3).toUpperCase();
  return (words[0][0] + words[1][0]).toUpperCase();
}

function focusFirstOption() {
  var first = document.querySelector('.admin-rich-select[data-open="true"] .admin-rich-option');
  if (first) first.focus();
}

function focusSiblingOption(current, direction) {
  var options = Array.prototype.slice.call(current.parentNode.querySelectorAll('.admin-rich-option'));
  var index = options.indexOf(current);
  var next = options[index + direction];
  if (!next && direction > 0) next = options[0];
  if (!next && direction < 0) next = options[options.length - 1];
  if (next) next.focus();
}
