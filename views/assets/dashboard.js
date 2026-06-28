document.addEventListener('DOMContentLoaded', function () {
  initCSRFProtection();
  document.addEventListener('click', handleTestWebhookClick);
  document.addEventListener('click', handleCopyClick);
  document.addEventListener('click', handleGenerateSecretClick);
  initPaymentLinkTypeToggle();
  initDashboardModals();
  initMerchantProductsTabs();
  initAdminRichSelects();
  initAdminDataTables();
  initRecoverFundsBalance();
  initDashboardActiveTabScroll();
});

function handleTestWebhookClick(event) {
  var btn = event.target.closest('[data-test-webhook]');
  if (!btn) return;

  var domainID = btn.getAttribute('data-test-webhook');
  var resultEl = document.getElementById('webhook-result-' + domainID);
  if (!resultEl) return;

  btn.disabled = true;
  btn.textContent = 'Gönderiliyor...';
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
        resultEl.textContent = 'HTTP ' + data.status_code + ' - webhook başarıyla iletildi.';
      } else {
        resultEl.classList.add('border-red-200', 'bg-red-50', 'text-red-800');
        resultEl.textContent = data.error || ('HTTP ' + data.status_code);
        if (data.response) resultEl.textContent += ' - ' + data.response.slice(0, 200);
      }
    })
    .catch(function (err) {
      resultEl.classList.remove('hidden');
      resultEl.classList.add('border-red-200', 'bg-red-50', 'text-red-800');
      resultEl.textContent = 'İstek başarısız: ' + err.message;
    })
    .finally(function () {
      btn.disabled = false;
      btn.textContent = 'Webhook Test Et';
    });
}

function handleCopyClick(event) {
  var button = event.target.closest('[data-copy-target], [data-copy-value]');
  if (!button) return;

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
}

function handleGenerateSecretClick(event) {
  var button = event.target.closest('[data-generate-secret]');
  if (!button) return;

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
}

function initPaymentLinkTypeToggle() {
  document.querySelectorAll('[data-payment-link-type]').forEach(function (select) {
    var form = select.closest('form') || document;
    var fixedFields = form.querySelector('[data-payment-fixed-fields]');
    var requiredInputs = Array.prototype.slice.call(form.querySelectorAll('[data-required-when-fixed]'));

    function update() {
      var donation = select.value === 'donation';
      if (fixedFields) {
        fixedFields.classList.toggle('hidden', donation);
      }
      requiredInputs.forEach(function (input) {
        input.required = !donation;
        input.disabled = donation;
      });
      var currency = form.querySelector('#product_currency') || form.querySelector('[name="currency"]');
      if (currency) {
        currency.disabled = donation;
      }
    }

    select.addEventListener('change', update);
    update();
  });
}

function initDashboardModals() {
  var lastOpenButton = null;

  function getModalByName(name) {
    var modals = Array.prototype.slice.call(document.querySelectorAll('[data-dashboard-modal]'));
    for (var i = 0; i < modals.length; i += 1) {
      if (modals[i].getAttribute('data-dashboard-modal') === name) {
        return modals[i];
      }
    }
    return null;
  }

  function anyOpenModal() {
    return document.querySelector('.merchant-modal[data-open="true"]');
  }

  function closeModal(modal, restoreFocus) {
    if (!modal) return;
    modal.hidden = true;
    modal.removeAttribute('data-open');
    if (!anyOpenModal()) {
      document.body.classList.remove('merchant-modal-open');
    }
    if (restoreFocus && lastOpenButton && document.body.contains(lastOpenButton)) {
      lastOpenButton.focus();
    }
  }

  function openModal(name, opener) {
    var modal = getModalByName(name);
    if (!modal) return;

    Array.prototype.slice.call(document.querySelectorAll('.merchant-modal[data-open="true"]')).forEach(function (open) {
      closeModal(open, false);
    });

    lastOpenButton = opener || null;
    modal.hidden = false;
    modal.setAttribute('data-open', 'true');
    document.body.classList.add('merchant-modal-open');

    window.setTimeout(function () {
      var target = modal.querySelector('input:not([type="hidden"]):not(:disabled), select:not(:disabled), textarea:not(:disabled), button:not(:disabled)');
      if (target) {
        target.focus();
      }
    }, 20);
  }

  document.querySelectorAll('[data-open-dashboard-modal]').forEach(function (button) {
    button.addEventListener('click', function () {
      if (button.disabled) return;
      openModal(button.getAttribute('data-open-dashboard-modal'), button);
    });
  });

  document.addEventListener('click', function (event) {
    var closeButton = event.target.closest('[data-close-dashboard-modal]');
    if (!closeButton) return;
    closeModal(closeButton.closest('[data-dashboard-modal]'), true);
  });

  document.addEventListener('keydown', function (event) {
    if (event.key !== 'Escape') return;
    var modal = anyOpenModal();
    if (modal) {
      closeModal(modal, true);
    }
  });
}

function initMerchantProductsTabs() {
  document.querySelectorAll('[data-products-workspace]').forEach(function (workspace, workspaceIndex) {
    var tabs = Array.prototype.slice.call(workspace.querySelectorAll('[data-products-tab]'));
    var panels = Array.prototype.slice.call(workspace.querySelectorAll('[data-products-panel]'));
    if (!tabs.length || !panels.length) return;

    tabs.forEach(function (tab, index) {
      var name = tab.getAttribute('data-products-tab');
      var panel = null;
      for (var i = 0; i < panels.length; i += 1) {
        if (panels[i].getAttribute('data-products-panel') === name) {
          panel = panels[i];
          break;
        }
      }
      if (!panel) return;

      var tabID = tab.id || 'merchant-products-tab-' + workspaceIndex + '-' + index;
      var panelID = panel.id || 'merchant-products-panel-' + workspaceIndex + '-' + index;
      tab.id = tabID;
      panel.id = panelID;
      tab.setAttribute('aria-controls', panelID);
      panel.setAttribute('role', 'tabpanel');
      panel.setAttribute('aria-labelledby', tabID);
      tab.setAttribute('tabindex', tab.getAttribute('aria-selected') === 'true' ? '0' : '-1');
    });

    function activate(name, focusTab) {
      tabs.forEach(function (tab) {
        var selected = tab.getAttribute('data-products-tab') === name;
        tab.setAttribute('aria-selected', selected ? 'true' : 'false');
        tab.setAttribute('tabindex', selected ? '0' : '-1');
        if (selected && focusTab) {
          tab.focus();
        }
      });
      panels.forEach(function (panel) {
        panel.hidden = panel.getAttribute('data-products-panel') !== name;
      });
    }

    tabs.forEach(function (tab, index) {
      tab.addEventListener('click', function () {
        activate(tab.getAttribute('data-products-tab'), false);
      });
      tab.addEventListener('keydown', function (event) {
        var nextIndex = index;
        if (event.key === 'ArrowRight') nextIndex = (index + 1) % tabs.length;
        if (event.key === 'ArrowLeft') nextIndex = (index - 1 + tabs.length) % tabs.length;
        if (event.key === 'Home') nextIndex = 0;
        if (event.key === 'End') nextIndex = tabs.length - 1;
        if (nextIndex === index) return;
        event.preventDefault();
        activate(tabs[nextIndex].getAttribute('data-products-tab'), true);
      });
    });

    var selectedTab = workspace.querySelector('[data-products-tab][aria-selected="true"]') || tabs[0];
    activate(selectedTab.getAttribute('data-products-tab'), false);
  });
}

function initDashboardActiveTabScroll() {
  var activeTab = document.querySelector('.dash-tab[aria-selected="true"]');
  if (!activeTab) return;

  var scroller = activeTab.closest('.overflow-x-auto') || activeTab.parentElement;
  if (!scroller || scroller.scrollWidth <= scroller.clientWidth) return;

  window.requestAnimationFrame(function () {
    var left = activeTab.offsetLeft - ((scroller.clientWidth - activeTab.offsetWidth) / 2);
    scroller.scrollTo({ left: Math.max(0, left), behavior: 'auto' });
  });
}

function initCSRFProtection() {
  attachCSRFInputs();
  patchCSRFFetch();
}

function csrfCookie(name) {
  var prefix = name + '=';
  var cookies = document.cookie ? document.cookie.split(';') : [];
  for (var i = 0; i < cookies.length; i += 1) {
    var cookie = cookies[i].trim();
    if (cookie.indexOf(prefix) === 0) {
      return decodeURIComponent(cookie.slice(prefix.length));
    }
  }
  return '';
}

function csrfToken() {
  return csrfCookie('gateway_csrf_jwt');
}

function attachCSRFInputs() {
  var token = csrfToken();
  if (!token) return;
  document.querySelectorAll('form').forEach(function (form) {
    var method = (form.getAttribute('method') || 'get').toLowerCase();
    if (method !== 'post') return;
    var input = form.querySelector('input[name="_csrf"]');
    if (!input) {
      input = document.createElement('input');
      input.type = 'hidden';
      input.name = '_csrf';
      form.appendChild(input);
    }
    input.value = token;
  });
}

function patchCSRFFetch() {
  if (!window.fetch || window.fetch.__csrfPatched) return;
  var originalFetch = window.fetch;
  window.fetch = function (input, init) {
    init = init || {};
    var method = (init.method || (input && input.method) || 'GET').toUpperCase();
    if (method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS') {
      var url = typeof input === 'string' ? input : (input && input.url) || '';
      var sameOrigin = !url || url.indexOf('/') === 0 || url.indexOf(window.location.origin) === 0;
      var token = csrfToken();
      if (sameOrigin && token) {
        var headers = new Headers(init.headers || (input && input.headers) || {});
        if (!headers.has('X-CSRF-Token')) {
          headers.set('X-CSRF-Token', token);
        }
        init.headers = headers;
        if (!init.credentials) {
          init.credentials = 'same-origin';
        }
      }
    }
    return originalFetch(input, init);
  };
  window.fetch.__csrfPatched = true;
}

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
    var preview = null;

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
    if (kind === 'wallet') {
      preview = document.createElement('div');
      preview.className = 'admin-rich-preview';
      preview.setAttribute('data-visible', 'false');
      root.appendChild(preview);
    }

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

      var lastGroup = null;
      records.forEach(function (record) {
        if (record.group && record.group !== lastGroup) {
          lastGroup = record.group;
          var group = document.createElement('div');
          group.className = 'admin-rich-group';
          group.textContent = record.group;
          optionsEl.appendChild(group);
        }
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
      updatePreview();
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
    updatePreview();

    function updatePreview() {
      if (!preview) return;
      var record = selectedRecord();
      if (!record) {
        preview.textContent = '';
        preview.setAttribute('data-visible', 'false');
        return;
      }
      preview.textContent = '';
      preview.setAttribute('data-visible', 'true');

      var head = document.createElement('div');
      var copy = document.createElement('div');
      var title = document.createElement('p');
      var subtitle = document.createElement('p');
      var badge = document.createElement('span');
      var grid = document.createElement('div');

      head.className = 'admin-rich-preview-head';
      title.className = 'admin-rich-preview-title';
      subtitle.className = 'admin-rich-preview-subtitle';
      badge.className = 'admin-rich-preview-badge';
      grid.className = 'admin-rich-preview-grid';

      title.textContent = record.details.merchant || record.primary;
      subtitle.textContent = record.details.domain || record.meta;
      badge.textContent = record.details.kind || record.chip;

      copy.appendChild(title);
      copy.appendChild(subtitle);
      head.appendChild(copy);
      if (badge.textContent) head.appendChild(badge);
      preview.appendChild(head);

      [
        ['Domain ID', record.details.domainID],
        ['Wallet', record.details.wallet || record.value],
        ['Owner', record.details.owner],
        ['Wallet ID', record.value],
      ].forEach(function (item) {
        if (!item[1]) return;
        var cell = document.createElement('div');
        var key = document.createElement('span');
        var value = document.createElement('span');
        cell.className = 'admin-rich-preview-item';
        key.className = 'admin-rich-preview-key';
        value.className = 'admin-rich-preview-value';
        key.textContent = item[0];
        value.textContent = item[1];
        value.title = item[1];
        cell.appendChild(key);
        cell.appendChild(value);
        grid.appendChild(cell);
      });
      preview.appendChild(grid);
    }
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

function initRecoverFundsBalance() {
  var form = document.getElementById('recover-form') || document.getElementById('sweep-form');
  if (!form) return;

  var walletSelect = document.getElementById('recover-source-wallet');
  var assetSelect = form.querySelector('select[name="asset"]');
  var amountInput = document.getElementById('recover-amount-raw');
  var maxButton = document.getElementById('recover-max-button');
  var balanceDisplay = document.getElementById('recover-balance-display');
  var balanceRaw = document.getElementById('recover-balance-raw');
  var liveBalanceDisplay = document.getElementById('recover-live-balance-display');
  var liveBalanceRaw = document.getElementById('recover-live-balance-raw');
  var liveRefreshButton = document.getElementById('recover-live-refresh-button');
  var balanceState = document.getElementById('recover-balance-state');
  if (!walletSelect || !assetSelect || !amountInput || !maxButton || !balanceDisplay || !balanceRaw || !liveBalanceDisplay || !liveBalanceRaw || !liveRefreshButton || !balanceState) return;

  var balances = {};
  document.querySelectorAll('[data-recover-balance]').forEach(function (node) {
    var walletID = compactText(node.getAttribute('data-wallet-id') || '');
    var assetKey = normalizeAssetKey(node.getAttribute('data-asset-key') || '');
    if (!walletID || !assetKey) return;
    balances[walletID + '::' + assetKey] = {
      chain: compactText(node.getAttribute('data-chain') || ''),
      symbol: compactText(node.getAttribute('data-symbol') || ''),
      available: compactText(node.getAttribute('data-available') || '0'),
      availableRaw: compactText(node.getAttribute('data-available-raw') || '0'),
      locked: compactText(node.getAttribute('data-locked') || ''),
    };
  });

  function selectedAssetOption() {
    return assetSelect.options[assetSelect.selectedIndex] || null;
  }

  function selectedAssetKey() {
    var option = selectedAssetOption();
    if (!option || !option.value) return '';
    return normalizeAssetKey(option.getAttribute('data-balance-key') || option.value);
  }

  function selectedAssetSymbol() {
    var option = selectedAssetOption();
    if (!option || !option.value) return '';
    return compactText(option.getAttribute('data-primary') || option.textContent || '');
  }

  function updateRecoverBalance() {
    var walletID = compactText(walletSelect.value || '');
    var assetKey = selectedAssetKey();
    var symbol = selectedAssetSymbol();
    maxButton.disabled = true;
    liveRefreshButton.disabled = true;
    maxButton.removeAttribute('data-max-raw');
    resetRecoverLiveBalance('Canlı chain: -', 'Raw: -');

    if (!walletID || !assetKey) {
      balanceDisplay.textContent = 'Wallet ve asset seç';
      balanceRaw.textContent = 'Raw: -';
      balanceState.textContent = 'Bekliyor';
      return;
    }

    liveRefreshButton.disabled = false;
    resetRecoverLiveBalance('Canlı chain: Refresh ile oku', 'Raw: -');
    var balance = balances[walletID + '::' + assetKey] || {
      chain: '',
      symbol: symbol,
      available: '0',
      availableRaw: '0',
      locked: '',
    };
    var label = balance.symbol || symbol || 'Asset';
    balanceDisplay.textContent = balance.available + ' ' + label;
    balanceRaw.textContent = 'Raw: ' + balance.availableRaw + (balance.locked ? ' · Locked: ' + balance.locked : '');

    if (isPositiveIntegerString(balance.availableRaw)) {
      balanceState.textContent = 'Kullanılabilir';
      maxButton.disabled = false;
      maxButton.setAttribute('data-max-raw', balance.availableRaw);
      return;
    }

    balanceState.textContent = 'Bakiye yok';
  }

  var liveBalanceRequestID = 0;

  function resetRecoverLiveBalance(display, raw) {
    liveBalanceDisplay.textContent = display;
    liveBalanceRaw.textContent = raw;
  }

  function fetchRecoverLiveBalance() {
    var walletID = compactText(walletSelect.value || '');
    var option = selectedAssetOption();
    var assetValue = option && option.value ? option.value : '';
    var requestID = liveBalanceRequestID + 1;
    liveBalanceRequestID = requestID;

    if (!walletID || !assetValue) {
      resetRecoverLiveBalance('Canlı chain: -', 'Raw: -');
      liveRefreshButton.disabled = true;
      return;
    }

    liveRefreshButton.disabled = true;
    resetRecoverLiveBalance('Canlı chain: sorgulanıyor...', 'Raw: -');
    var params = new URLSearchParams();
    params.set('wallet_id', walletID);
    params.set('asset', assetValue);

    fetch('/admin/recover/live-balance?' + params.toString(), {
      credentials: 'same-origin',
      headers: { Accept: 'application/json' },
    })
      .then(function (response) {
        return response.json().then(function (payload) {
          if (!response.ok || !payload || payload.result !== 'success') {
            throw new Error((payload && payload.message) || 'Canlı bakiye okunamadı');
          }
          return payload;
        });
      })
      .then(function (payload) {
        if (requestID !== liveBalanceRequestID) return;
        var symbol = compactText(payload.symbol || selectedAssetSymbol() || 'Asset');
        var formatted = compactText(payload.balance || '');
        var raw = compactText(payload.balance_raw || '0');
        var address = compactText(payload.address || '');
        resetRecoverLiveBalance('Canlı chain: ' + formatted + ' ' + symbol, 'Raw: ' + raw + (address ? ' · ' + address : ''));
        liveRefreshButton.disabled = false;
      })
      .catch(function (error) {
        if (requestID !== liveBalanceRequestID) return;
        resetRecoverLiveBalance('Canlı chain: okunamadı', compactText(error.message || 'Bilinmeyen hata'));
        liveRefreshButton.disabled = false;
      });
  }

  walletSelect.addEventListener('change', updateRecoverBalance);
  assetSelect.addEventListener('change', updateRecoverBalance);
  liveRefreshButton.addEventListener('click', fetchRecoverLiveBalance);
  maxButton.addEventListener('click', function () {
    var maxRaw = maxButton.getAttribute('data-max-raw') || '';
    if (!maxRaw) return;
    amountInput.value = maxRaw;
    amountInput.dispatchEvent(new Event('input', { bubbles: true }));
    amountInput.dispatchEvent(new Event('change', { bubbles: true }));
    amountInput.focus();
  });

  updateRecoverBalance();
}

function initAdminDataTables() {
  document.querySelectorAll('table[data-admin-table]').forEach(function (table) {
    if (table.getAttribute('data-admin-table-ready') === 'true') return;
    table.setAttribute('data-admin-table-ready', 'true');

    var tableID = compactText(table.id || '');
    var tbody = table.tBodies[0];
    if (!tableID || !tbody) return;

    var rows = Array.prototype.slice.call(tbody.querySelectorAll('tr[data-admin-table-row]'));
    var emptyRow = tbody.querySelector('tr[data-admin-table-empty]');
    var detailRows = {};
    var searchInput = findAdminDataTableControl('data-admin-table-search', tableID);
    var countEl = findAdminDataTableControl('data-admin-table-count', tableID);
    var sortState = {
      column: -1,
      direction: 'none',
      type: 'text',
    };

    Array.prototype.slice.call(tbody.querySelectorAll('tr[data-admin-table-detail-for]')).forEach(function (row) {
      var key = row.getAttribute('data-admin-table-detail-for') || '';
      if (key) detailRows[key] = row;
    });

    rows.forEach(function (row, index) {
      row.setAttribute('data-admin-table-index', String(index));
      row.setAttribute('data-admin-row-expanded', 'false');
      row.setAttribute('data-admin-table-search-value', normalize((row.getAttribute('data-search') || '') + ' ' + row.textContent));
    });

    Array.prototype.slice.call(table.querySelectorAll('thead th')).forEach(function (th, index) {
      var button = th.querySelector('.admin-sort-button[data-admin-sort]');
      if (!button) return;
      th.setAttribute('aria-sort', 'none');
      button.setAttribute('data-sort-direction', 'none');
      button.addEventListener('click', function () {
        var nextDirection = 'asc';
        if (sortState.column === index && sortState.direction === 'asc') {
          nextDirection = 'desc';
        }
        sortState = {
          column: index,
          direction: nextDirection,
          type: button.getAttribute('data-admin-sort') || 'text',
        };
        renderAdminDataTable(table, tbody, rows, detailRows, emptyRow, countEl, sortState, searchInput ? searchInput.value : '');
      });
    });

    table.addEventListener('click', function (event) {
      handleAdminDataTableToggle(event, detailRows);
    });

    if (searchInput) {
      searchInput.addEventListener('input', function () {
        renderAdminDataTable(table, tbody, rows, detailRows, emptyRow, countEl, sortState, searchInput.value);
      });
    }

    renderAdminDataTable(table, tbody, rows, detailRows, emptyRow, countEl, sortState, searchInput ? searchInput.value : '');
  });
}

function renderAdminDataTable(table, tbody, rows, detailRows, emptyRow, countEl, sortState, query) {
  var normalizedQuery = normalize(query || '');
  var visibleCount = 0;
  var sortedRows = rows.slice();

  if (sortState.column >= 0 && sortState.direction !== 'none') {
    sortedRows.sort(function (left, right) {
      var comparison = compareAdminDataTableRows(left, right, sortState);
      if (comparison === 0) {
        comparison = Number(left.getAttribute('data-admin-table-index') || '0') - Number(right.getAttribute('data-admin-table-index') || '0');
      }
      return sortState.direction === 'desc' ? -comparison : comparison;
    });
  }

  sortedRows.forEach(function (row) {
    if (emptyRow) {
      tbody.insertBefore(row, emptyRow);
    } else {
      tbody.appendChild(row);
    }
    var key = row.getAttribute('data-admin-table-key') || '';
    var detailRow = key ? detailRows[key] : null;
    if (detailRow) {
      if (emptyRow) {
        tbody.insertBefore(detailRow, emptyRow);
      } else {
        tbody.appendChild(detailRow);
      }
    }

    var searchValue = row.getAttribute('data-admin-table-search-value') || '';
    var hidden = normalizedQuery !== '' && searchValue.indexOf(normalizedQuery) === -1;
    row.hidden = hidden;
    if (detailRow) {
      detailRow.hidden = hidden || row.getAttribute('data-admin-row-expanded') !== 'true';
    }
    if (!hidden) visibleCount += 1;
  });

  if (emptyRow) {
    emptyRow.hidden = rows.length === 0 || visibleCount > 0;
  }

  updateAdminDataTableCount(countEl, visibleCount, rows.length, normalizedQuery !== '');
  updateAdminDataTableSortControls(table, sortState);
}

function handleAdminDataTableToggle(event, detailRows) {
  var button = event.target.closest('[data-admin-table-toggle]');
  if (!button) return;

  var row = button.closest('tr[data-admin-table-row]');
  if (!row) return;

  var key = button.getAttribute('data-admin-table-toggle') || row.getAttribute('data-admin-table-key') || '';
  var detailRow = key ? detailRows[key] : null;
  if (!detailRow) return;

  var expanded = row.getAttribute('data-admin-row-expanded') !== 'true';
  row.setAttribute('data-admin-row-expanded', expanded ? 'true' : 'false');
  button.setAttribute('aria-expanded', expanded ? 'true' : 'false');
  button.setAttribute('data-expanded', expanded ? 'true' : 'false');
  detailRow.hidden = row.hidden || !expanded;
}

function compareAdminDataTableRows(left, right, sortState) {
  var leftValue = adminDataTableCellValue(left, sortState.column);
  var rightValue = adminDataTableCellValue(right, sortState.column);
  if (sortState.type === 'number') {
    return compareIntegerStrings(leftValue, rightValue);
  }
  return normalize(leftValue).localeCompare(normalize(rightValue), 'tr', {
    numeric: true,
    sensitivity: 'base',
  });
}

function adminDataTableCellValue(row, column) {
  var cell = row.children[column];
  if (!cell) return '';
  var value = cell.getAttribute('data-sort-value');
  if (value === null) value = cell.textContent || '';
  return compactText(value);
}

function updateAdminDataTableSortControls(table, sortState) {
  Array.prototype.slice.call(table.querySelectorAll('thead th')).forEach(function (th, index) {
    var button = th.querySelector('.admin-sort-button[data-admin-sort]');
    var direction = index === sortState.column ? sortState.direction : 'none';
    th.setAttribute('aria-sort', direction === 'asc' ? 'ascending' : (direction === 'desc' ? 'descending' : 'none'));
    if (button) {
      button.setAttribute('data-sort-direction', direction);
    }
  });
}

function updateAdminDataTableCount(countEl, visibleCount, totalCount, filtered) {
  if (!countEl) return;
  var label = visibleCount + ' asset';
  if (filtered && totalCount !== visibleCount) {
    label = visibleCount + ' / ' + totalCount + ' asset';
  }
  countEl.textContent = label;
}

function findAdminDataTableControl(attribute, tableID) {
  var nodes = document.querySelectorAll('[' + attribute + ']');
  for (var i = 0; i < nodes.length; i += 1) {
    if (nodes[i].getAttribute(attribute) === tableID) {
      return nodes[i];
    }
  }
  return null;
}

function compareIntegerStrings(left, right) {
  var a = normalizeIntegerString(left);
  var b = normalizeIntegerString(right);

  if (typeof BigInt === 'function') {
    try {
      var leftBig = BigInt(a);
      var rightBig = BigInt(b);
      if (leftBig < rightBig) return -1;
      if (leftBig > rightBig) return 1;
      return 0;
    } catch (error) {
      // Fall through to string comparison for environments without full BigInt parsing.
    }
  }

  var aNegative = a.charAt(0) === '-';
  var bNegative = b.charAt(0) === '-';
  if (aNegative !== bNegative) return aNegative ? -1 : 1;

  var aValue = aNegative ? a.slice(1) : a;
  var bValue = bNegative ? b.slice(1) : b;
  var comparison = 0;
  if (aValue.length !== bValue.length) {
    comparison = aValue.length < bValue.length ? -1 : 1;
  } else if (aValue < bValue) {
    comparison = -1;
  } else if (aValue > bValue) {
    comparison = 1;
  }
  return aNegative ? -comparison : comparison;
}

function normalizeIntegerString(value) {
  var raw = compactText(value);
  if (!/^-?[0-9]+$/.test(raw)) return '0';
  var negative = raw.charAt(0) === '-';
  if (negative) raw = raw.slice(1);
  raw = raw.replace(/^0+/, '');
  if (raw === '') return '0';
  return negative ? '-' + raw : raw;
}

function normalizeAssetKey(value) {
  return compactText(value).toLocaleLowerCase('tr-TR');
}

function isPositiveIntegerString(value) {
  var raw = compactText(value);
  return /^[0-9]+$/.test(raw) && raw.replace(/^0+/, '') !== '';
}

function readOption(option) {
  var text = compactText(option.textContent || '');
  var primary = option.getAttribute('data-primary') || text;
  var meta = option.getAttribute('data-meta') || text;
  var chip = option.getAttribute('data-chip') || '';
  var avatarSource = option.getAttribute('data-avatar') || primary;
  var group = compactText(option.getAttribute('data-group') || '');
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
    group: group,
    avatar: initials(avatarSource),
    search: normalize(search),
    details: {
      domain: compactText(option.getAttribute('data-detail-domain') || ''),
      domainID: compactText(option.getAttribute('data-detail-domain-id') || ''),
      merchant: compactText(option.getAttribute('data-detail-merchant') || ''),
      wallet: compactText(option.getAttribute('data-detail-wallet') || ''),
      owner: compactText(option.getAttribute('data-detail-owner') || ''),
      kind: compactText(option.getAttribute('data-detail-kind') || ''),
    },
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
