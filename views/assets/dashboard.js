document.addEventListener('DOMContentLoaded', function () {
  document.addEventListener('click', handleTestWebhookClick);
  document.addEventListener('click', handleRotateAPISecretClick);
  document.addEventListener('click', handleCopyClick);
  document.addEventListener('click', handleGenerateSecretClick);
  initMerchantWorkspace();
  initMerchantFastNavigation();
});

function initMerchantWorkspace() {
  initPortalJWTProtection();
  initPaymentLinkTypeToggle();
  initNotificationModeToggle();
  initPaymentLinkWizard();
  initMerchantProductEditModal();
  initDashboardModals();
  initMerchantProductsTabs();
  initAdminRichSelects();
  initAdminDataTables();
  initRecoverFundsBalance();
  initMerchantWithdrawalBalance();
  initMerchantChainVisibility();
  initDashboardActiveTabScroll();
  initMerchantRescanFeedback();
}

function initMerchantFastNavigation() {
  if (!document.body.classList.contains('merchant-shell-body')) return;
  if (document.documentElement.getAttribute('data-merchant-fast-nav-ready') === 'true') return;
  document.documentElement.setAttribute('data-merchant-fast-nav-ready', 'true');

  var cache = new Map();
  var cacheTTL = 10000;
  var navigationSequence = 0;

  function routeURL(anchor) {
    if (!anchor || !anchor.href || anchor.target || anchor.hasAttribute('download')) return null;
    var url;
    try {
      url = new URL(anchor.href, window.location.href);
    } catch (error) {
      return null;
    }
    if (url.origin !== window.location.origin) return null;
    if (!/^\/merchant\/(?:dashboard(?:\/|$)|domains\/?$)/.test(url.pathname)) return null;
    return url;
  }

  function loadRoute(url) {
    var key = url.pathname + url.search;
    var cached = cache.get(key);
    if (cached && Date.now() - cached.createdAt < cacheTTL) {
      return cached.promise;
    }

    var promise = fetch(key, {
      method: 'GET',
      credentials: 'same-origin',
      headers: {
        'Accept': 'text/html',
        'X-Merchant-Navigation': 'partial',
      },
    }).then(function (response) {
      if (!response.ok) throw new Error('Route yüklenemedi: HTTP ' + response.status);
      return response.text();
    }).catch(function (error) {
      cache.delete(key);
      throw error;
    });

    cache.set(key, { createdAt: Date.now(), promise: promise });
    if (cache.size > 12) {
      cache.delete(cache.keys().next().value);
    }
    return promise;
  }

  function markPending(anchor) {
    document.body.setAttribute('data-merchant-route-loading', 'true');
    if (!anchor || !anchor.classList.contains('dash-tab')) return;
    document.querySelectorAll('.merchant-dashboard-tabs .dash-tab[aria-current="page"]').forEach(function (tab) {
      tab.removeAttribute('aria-current');
    });
    anchor.setAttribute('aria-current', 'page');
  }

  function finishPending() {
    document.body.removeAttribute('data-merchant-route-loading');
  }

  function renderRoute(url, html, pushHistory) {
    var parsed = new DOMParser().parseFromString(html, 'text/html');
    var incoming = parsed.querySelector('.merchant-workbench');
    var current = document.querySelector('.merchant-workbench');
    if (!incoming || !current) throw new Error('Merchant workspace bulunamadı.');

    document.body.classList.remove('merchant-modal-open');
    current.querySelectorAll('.adm-wrap.kewl-grid').forEach(function (grid) {
      if (typeof grid._adminGridCleanup === 'function') grid._adminGridCleanup();
    });
    document.querySelectorAll('.admin-rich-menu[data-floating="true"]').forEach(function (menu) {
      menu.remove();
    });
    current.replaceWith(incoming);
    if (parsed.title) document.title = parsed.title;
    if (pushHistory) {
      window.history.pushState({ merchantRoute: true }, '', url.pathname + url.search + url.hash);
    }
    initMerchantWorkspace();
    window.scrollTo({ top: 0, left: 0, behavior: 'auto' });
    document.dispatchEvent(new CustomEvent('merchant:route-ready', {
      detail: { url: url.pathname + url.search },
    }));
  }

  function navigate(url, anchor, pushHistory) {
    var sequence = ++navigationSequence;
    markPending(anchor);
    return loadRoute(url).then(function (html) {
      if (sequence !== navigationSequence) return;
      renderRoute(url, html, pushHistory);
      finishPending();
    }).catch(function () {
      if (sequence !== navigationSequence) return;
      finishPending();
      if (pushHistory) {
        window.location.assign(url.href);
      } else {
        window.location.reload();
      }
    });
  }

  function prefetchFromEvent(event) {
    var anchor = event.target.closest('a');
    var url = routeURL(anchor);
    if (url) loadRoute(url).catch(function () {});
  }

  document.addEventListener('pointerover', prefetchFromEvent, { passive: true });
  document.addEventListener('focusin', prefetchFromEvent);
  document.addEventListener('click', function (event) {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    var anchor = event.target.closest('a');
    var url = routeURL(anchor);
    if (!url) return;
    if (url.pathname === window.location.pathname && url.search === window.location.search && !url.hash) return;
    event.preventDefault();
    navigate(url, anchor, true);
  });
  window.addEventListener('popstate', function () {
    var url = new URL(window.location.href);
    if (!/^\/merchant\/(?:dashboard(?:\/|$)|domains\/?$)/.test(url.pathname)) {
      window.location.reload();
      return;
    }
    navigate(url, null, false);
  });
}

function handleTestWebhookClick(event) {
  var btn = event.target.closest('[data-test-webhook]');
  if (!btn) return;

	var domainID = btn.getAttribute('data-test-webhook');
	var notificationMode = btn.getAttribute('data-notification-mode') || 'webhook';
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
		resultEl.textContent = notificationMode === 'nats'
		  ? 'NATS subject\'ine test mesajı başarıyla publish edildi.'
		  : 'HTTP ' + data.status_code + ' - webhook başarıyla iletildi.';
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
		btn.textContent = 'Test et';
	});
}

function initNotificationModeToggle() {
  document.querySelectorAll('[data-notification-mode]').forEach(function (select) {
    var form = select.closest('form') || document;
    var webhookPanel = form.querySelector('[data-notification-webhook]');
    var natsPanel = form.querySelector('[data-notification-nats]');
    if (!webhookPanel || !natsPanel) return;

    function setPanel(panel, active) {
      panel.hidden = !active;
      panel.querySelectorAll('input, select, textarea').forEach(function (input) {
        input.disabled = !active;
      });
    }

    function update() {
      var nats = select.value === 'nats';
      setPanel(webhookPanel, !nats);
      setPanel(natsPanel, nats);
      var webhookURL = webhookPanel.querySelector('[name="webhook_url"]');
      var webhookSecret = webhookPanel.querySelector('[name="webhook_secret"]');
      var natsURL = natsPanel.querySelector('[name="nats_url"]');
      if (webhookURL) webhookURL.required = !nats;
      if (webhookSecret) webhookSecret.required = !nats && !webhookSecret.getAttribute('placeholder');
      if (natsURL) natsURL.required = nats;
    }

    select.addEventListener('change', update);
    update();
  });
}

function handleRotateAPISecretClick(event) {
  var btn = event.target.closest('[data-rotate-api-secret]');
  if (!btn) return;

  var domainID = btn.getAttribute('data-rotate-api-secret');
  var confirmation = btn.getAttribute('data-rotate-confirm') || '';
  var resultEl = document.getElementById('api-secret-result-' + domainID);
  if (!domainID || !confirmation || !resultEl) return;

  if (!window.confirm('API secret rotate edilecek. Mevcut secret hemen iptal edilir. Devam edilsin mi?')) {
    return;
  }

  btn.disabled = true;
  var originalText = btn.textContent;
  btn.textContent = 'Rotating...';
  resultEl.hidden = false;
  resultEl.className = 'merchant-secret-result';
  resultEl.textContent = 'Rotation isteği gönderildi.';

  var body = new URLSearchParams();
  body.set('confirm_rotate', confirmation);

  fetch('/merchant/domains/' + domainID + '/rotate-api-secret', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
      'X-Gateway-Rotate-Confirm': confirmation,
    },
    credentials: 'same-origin',
    body: body.toString(),
  })
    .then(function (response) {
      return response.json().then(function (data) {
        if (!response.ok) {
          throw new Error(data.error || data.message || 'Rotation başarısız');
        }
        return data;
      });
    })
    .then(function (data) {
      var secret = data.api_secret || '';
      resultEl.textContent = '';
      var title = document.createElement('strong');
      title.textContent = 'Yeni API secret bir kez gösteriliyor';
      var copy = document.createElement('button');
      copy.className = 'merchant-row-button';
      copy.type = 'button';
      copy.setAttribute('data-copy-value', secret);
      copy.textContent = 'Secret kopyala';
      var code = document.createElement('code');
      code.textContent = secret;
      var note = document.createElement('span');
      note.textContent = 'Bu değer tekrar gösterilmez. Secret manager tarafına kaydedin; eski secret hemen iptal edildi.';
      resultEl.appendChild(title);
      resultEl.appendChild(copy);
      resultEl.appendChild(code);
      resultEl.appendChild(note);
    })
    .catch(function (err) {
      resultEl.classList.add('is-error');
      resultEl.textContent = err.message;
    })
    .finally(function () {
      btn.disabled = false;
      btn.textContent = originalText;
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
    var fixedFields = Array.prototype.slice.call(form.querySelectorAll('[data-payment-fixed-fields]'));
    var requiredInputs = Array.prototype.slice.call(form.querySelectorAll('[data-required-when-fixed]'));
    var x402Input = form.querySelector('[name="x402_enabled"]');

    function update() {
      var donation = select.value === 'donation';
      fixedFields.forEach(function (field) {
        field.classList.toggle('hidden', donation);
      });
      requiredInputs.forEach(function (input) {
        input.required = !donation;
        input.disabled = donation;
      });
      var currency = form.querySelector('#product_currency') || form.querySelector('[name="currency"]');
      if (currency) {
        currency.disabled = donation;
      }
      if (x402Input) {
        x402Input.disabled = donation;
      }
    }

    select.addEventListener('change', update);
    update();
  });
}

function initMerchantProductEditModal() {
  var form = document.querySelector('[data-product-edit-form]');
  if (!form) return;

  document.querySelectorAll('[data-edit-product]').forEach(function (button) {
    if (button.getAttribute('data-product-edit-ready') === 'true') return;
    button.setAttribute('data-product-edit-ready', 'true');
    button.addEventListener('click', function () {
      var productID = compactText(button.getAttribute('data-edit-product') || '');
      if (!productID) return;
      form.action = '/merchant/products/' + encodeURIComponent(productID) + '/update';

      var values = {
        domain_id: button.getAttribute('data-product-domain-id') || '',
        name: button.getAttribute('data-product-name') || '',
        description: button.getAttribute('data-product-description') || '',
        logo_url: button.getAttribute('data-product-logo-url') || '',
        link_type: button.getAttribute('data-product-link-type') || 'fixed',
        language: (button.getAttribute('data-product-language') || 'tr').toLowerCase(),
        amount: button.getAttribute('data-product-amount') || '',
        currency: button.getAttribute('data-product-currency') || 'USD',
        default_asset: button.getAttribute('data-product-default-asset') || '',
        success_url: button.getAttribute('data-product-success-url') || '',
        cancel_url: button.getAttribute('data-product-cancel-url') || '',
      };

      Object.keys(values).forEach(function (name) {
        var field = form.elements.namedItem(name);
        if (!field) return;
        field.value = values[name];
        if (field.tagName === 'SELECT') {
          field.dispatchEvent(new Event('change', { bubbles: true }));
        }
      });

      var x402 = form.elements.namedItem('x402_enabled');
      if (x402) {
        x402.checked = button.getAttribute('data-product-x402') === 'true';
      }
    });
  });
}

function initMerchantChainVisibility() {
  document.querySelectorAll('[data-chain-visibility-form]').forEach(function (form) {
    if (form.getAttribute('data-chain-visibility-ready') === 'true') return;
    form.setAttribute('data-chain-visibility-ready', 'true');

    var visibleList = form.querySelector('[data-chain-list="visible"]');
    var hiddenList = form.querySelector('[data-chain-list="hidden"]');
    var hiddenInput = form.querySelector('[data-hidden-chains-input]');
    var testnetToggle = form.querySelector('[data-hide-testnets]');
    var live = form.querySelector('[data-chain-live]');
    var draggingCard = null;
    if (!visibleList || !hiddenList || !hiddenInput || !testnetToggle) return;

    function cards() {
      return Array.prototype.slice.call(form.querySelectorAll('[data-chain-card]'));
    }

    function isTrue(node, attribute) {
      return node.getAttribute(attribute) === 'true';
    }

    function isLocked(card) {
      return testnetToggle.checked && isTrue(card, 'data-chain-testnet');
    }

    function announce(message) {
      if (live) live.textContent = message;
    }

    function updateCounts() {
      ['visible', 'hidden'].forEach(function (name) {
        var list = name === 'visible' ? visibleList : hiddenList;
        var count = list.querySelectorAll('[data-chain-card]').length;
        var output = form.querySelector('[data-chain-count="' + name + '"]');
        var empty = list.querySelector('[data-chain-empty]');
        if (output) output.textContent = String(count);
        if (empty) empty.hidden = count !== 0;
      });
    }

    function serialize() {
      hiddenInput.value = cards().filter(function (card) {
        return isTrue(card, 'data-chain-explicit-hidden');
      }).map(function (card) {
        return card.getAttribute('data-chain-key') || '';
      }).filter(Boolean).join(',');
      updateCounts();
    }

    function updateCard(card) {
      var explicitHidden = isTrue(card, 'data-chain-explicit-hidden');
      var locked = isLocked(card);
      var policyHidden = locked && !explicitHidden;
      var shouldHide = explicitHidden || policyHidden;
      var targetList = shouldHide ? hiddenList : visibleList;
      if (card.parentElement !== targetList) targetList.appendChild(card);

      card.setAttribute('data-chain-policy-hidden', policyHidden ? 'true' : 'false');
      card.setAttribute('draggable', locked ? 'false' : 'true');
      card.classList.toggle('is-policy-locked', locked);

      var state = card.querySelector('.merchant-chain-state');
      if (state) {
        state.classList.toggle('is-visible', !shouldHide);
        state.classList.toggle('is-hidden', shouldHide);
        state.textContent = locked ? 'Politika' : (shouldHide ? 'Gizli' : 'Görünür');
      }
      var button = card.querySelector('[data-chain-toggle]');
      if (button) {
        button.disabled = locked;
        button.textContent = locked ? 'Kilitli' : (shouldHide ? 'Göster' : 'Gizle');
        button.setAttribute('aria-label', (shouldHide ? 'Göster: ' : 'Gizle: ') + (card.getAttribute('data-chain') || 'ağ'));
      }
    }

    function move(card, toHidden, shouldAnnounce) {
      if (!card) return;
      var chain = card.getAttribute('data-chain') || 'Ağ';
      if (!toHidden && isLocked(card)) {
        announce(chain + ' testnet politikası nedeniyle kilitli. Önce testnet filtresini kapatın.');
        return;
      }
      card.setAttribute('data-chain-explicit-hidden', toHidden ? 'true' : 'false');
      updateCard(card);
      serialize();
      if (shouldAnnounce) {
        announce(chain + (toHidden ? ' gizli ağlara taşındı.' : ' görünür ağlara taşındı.'));
      }
      card.focus({ preventScroll: true });
    }

    cards().forEach(function (card) {
      updateCard(card);
      var button = card.querySelector('[data-chain-toggle]');
      if (button) {
        button.addEventListener('click', function () {
          move(card, card.parentElement === visibleList, true);
        });
      }
      card.addEventListener('keydown', function (event) {
        if (!event.altKey || (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight')) return;
        event.preventDefault();
        move(card, event.key === 'ArrowRight', true);
      });
      card.addEventListener('dragstart', function (event) {
        if (isLocked(card)) {
          event.preventDefault();
          announce('Testnet politikasıyla kilitli ağ taşınamaz.');
          return;
        }
        draggingCard = card;
        card.classList.add('is-dragging');
        event.dataTransfer.effectAllowed = 'move';
        event.dataTransfer.setData('text/plain', card.getAttribute('data-chain-key') || '');
      });
      card.addEventListener('dragend', function () {
        draggingCard = null;
        card.classList.remove('is-dragging');
        form.querySelectorAll('[data-chain-zone]').forEach(function (zone) {
          zone.classList.remove('is-drag-over');
        });
      });
    });

    form.querySelectorAll('[data-chain-zone]').forEach(function (zone) {
      zone.addEventListener('dragover', function (event) {
        if (!draggingCard) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = 'move';
        zone.classList.add('is-drag-over');
      });
      zone.addEventListener('dragleave', function (event) {
        if (!zone.contains(event.relatedTarget)) zone.classList.remove('is-drag-over');
      });
      zone.addEventListener('drop', function (event) {
        event.preventDefault();
        zone.classList.remove('is-drag-over');
        move(draggingCard, zone.getAttribute('data-chain-zone') === 'hidden', true);
      });
    });

    testnetToggle.addEventListener('change', function () {
      cards().forEach(updateCard);
      serialize();
      announce(testnetToggle.checked ? 'Testnet politikası etkinleştirildi.' : 'Testnet politikası kapatıldı.');
    });
    form.addEventListener('submit', serialize);
    serialize();
  });
}

function initPaymentLinkWizard() {
  document.querySelectorAll('[data-payment-link-wizard]').forEach(function (form) {
    if (form.getAttribute('data-payment-wizard-ready') === 'true') return;
    form.setAttribute('data-payment-wizard-ready', 'true');

    var steps = Array.prototype.slice.call(form.querySelectorAll('[data-payment-wizard-step]'));
    var panels = Array.prototype.slice.call(form.querySelectorAll('[data-payment-wizard-panel]'));
    var prev = form.querySelector('[data-payment-wizard-prev]');
    var next = form.querySelector('[data-payment-wizard-next]');
    var submit = form.querySelector('[data-payment-wizard-submit]');
    var active = 0;

    function clamp(index) {
      return Math.max(0, Math.min(panels.length - 1, index));
    }

    function parseStep(value) {
      var parsed = parseInt(value, 10);
      return Number.isFinite(parsed) ? clamp(parsed) : 0;
    }

    function focusPanel(index) {
      var panel = panels[index];
      if (!panel) return;
      var target = panel.querySelector('input:not([type="hidden"]):not(:disabled), select:not(:disabled), textarea:not(:disabled), button:not(:disabled)');
      if (target) target.focus();
    }

    function setStep(index, shouldFocus) {
      active = clamp(index);
      form.setAttribute('data-payment-wizard-active', String(active));
      steps.forEach(function (step) {
        var selected = parseStep(step.getAttribute('data-payment-wizard-step')) === active;
        step.setAttribute('aria-selected', selected ? 'true' : 'false');
        step.tabIndex = selected ? 0 : -1;
      });
      panels.forEach(function (panel, panelIndex) {
        panel.hidden = panelIndex !== active;
      });
      if (prev) prev.hidden = active === 0;
      if (next) next.hidden = active === panels.length - 1;
      if (submit) submit.hidden = active !== panels.length - 1;
      if (shouldFocus) {
        window.setTimeout(function () { focusPanel(active); }, 0);
      }
    }

    function panelFields(index) {
      var panel = panels[index];
      if (!panel) return [];
      return Array.prototype.slice.call(panel.querySelectorAll('input, select, textarea')).filter(function (field) {
        return !field.disabled;
      });
    }

    function validatePanel(index) {
      var fields = panelFields(index);
      for (var i = 0; i < fields.length; i += 1) {
        if (!fields[i].checkValidity()) {
          setStep(index, false);
          window.setTimeout(function (field) {
            field.reportValidity();
            field.focus();
          }.bind(null, fields[i]), 0);
          return false;
        }
      }
      return true;
    }

    function canMoveTo(target) {
      if (target <= active) return true;
      for (var index = active; index < target; index += 1) {
        if (!validatePanel(index)) return false;
      }
      return true;
    }

    function firstInvalidPanel() {
      for (var index = 0; index < panels.length; index += 1) {
        var fields = panelFields(index);
        for (var fieldIndex = 0; fieldIndex < fields.length; fieldIndex += 1) {
          if (!fields[fieldIndex].checkValidity()) return index;
        }
      }
      return -1;
    }

    steps.forEach(function (step) {
      step.addEventListener('click', function () {
        var target = parseStep(step.getAttribute('data-payment-wizard-step'));
        if (canMoveTo(target)) setStep(target, true);
      });
    });

    if (prev) {
      prev.addEventListener('click', function () {
        setStep(active - 1, true);
      });
    }

    if (next) {
      next.addEventListener('click', function () {
        if (validatePanel(active)) setStep(active + 1, true);
      });
    }

    if (submit) {
      submit.addEventListener('click', function (event) {
        var invalidPanel = firstInvalidPanel();
        if (invalidPanel < 0) return;
        event.preventDefault();
        setStep(invalidPanel, false);
        window.setTimeout(function () { form.reportValidity(); }, 0);
      });
    }

    form.addEventListener('invalid', function (event) {
      var panel = event.target.closest('[data-payment-wizard-panel]');
      if (!panel) return;
      var index = panels.indexOf(panel);
      if (index >= 0) setStep(index, false);
    }, true);

    form.addEventListener('submit', function (event) {
      if (form.checkValidity()) return;
      var invalidPanel = firstInvalidPanel();
      if (invalidPanel >= 0) {
        event.preventDefault();
        setStep(invalidPanel, false);
        window.setTimeout(function () { form.reportValidity(); }, 0);
      }
    });

    var modal = form.closest('[data-dashboard-modal]');
    if (modal) {
      modal.addEventListener('dashboard:modal-open', function () {
        setStep(0, false);
      });
    }
    setStep(0, false);
  });
}

function initDashboardModals() {
  if (document.documentElement.getAttribute('data-dashboard-modals-ready') === 'true') return;
  document.documentElement.setAttribute('data-dashboard-modals-ready', 'true');
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
    modal.dispatchEvent(new CustomEvent('dashboard:modal-open', { bubbles: true }));

    window.setTimeout(function () {
      var target = modal.querySelector('.merchant-modal-form input:not([type="hidden"]):not(:disabled), .merchant-modal-form select:not(:disabled), .merchant-modal-form textarea:not(:disabled), .merchant-modal-form button:not(:disabled)') ||
        modal.querySelector('button:not([data-close-dashboard-modal]):not(:disabled)');
      if (target) {
        target.focus();
      }
    }, 20);
  }

  document.addEventListener('click', function (event) {
    var openButton = event.target.closest('[data-open-dashboard-modal]');
    if (openButton) {
      if (!openButton.disabled) {
        openModal(openButton.getAttribute('data-open-dashboard-modal'), openButton);
      }
      return;
    }
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

function initMerchantRescanFeedback() {
  document.querySelectorAll('[data-rescan-form]').forEach(function (form) {
    if (form.getAttribute('data-rescan-ready') === 'true') return;
    form.setAttribute('data-rescan-ready', 'true');

    var submit = form.querySelector('[data-rescan-submit]');
    var feedback = form.querySelector('[data-rescan-feedback]');
    var feedbackText = form.querySelector('[data-rescan-feedback-text]');
    var elapsed = form.querySelector('[data-rescan-elapsed]');
    var timer = null;

    form.addEventListener('submit', function () {
      if (typeof form.checkValidity === 'function' && !form.checkValidity()) return;

      var startedAt = Date.now();
      form.setAttribute('aria-busy', 'true');
      form.setAttribute('data-submitting', 'true');
      if (submit) {
        submit.disabled = true;
        submit.textContent = 'İşleniyor...';
      }
      if (feedback) feedback.hidden = false;
      if (feedbackText) feedbackText.textContent = 'İstek gönderildi; blockchain RPC yanıtı bekleniyor.';

      if (timer) window.clearInterval(timer);
      timer = window.setInterval(function () {
        var seconds = Math.max(0, Math.floor((Date.now() - startedAt) / 1000));
        if (elapsed) elapsed.textContent = seconds + ' sn';
        if (feedbackText && seconds >= 20) {
          feedbackText.textContent = 'RPC yanıtı bekleniyor; işlem devam ediyor.';
        }
      }, 1000);
    });
  });
}

function initMerchantProductsTabs() {
  document.querySelectorAll('[data-products-workspace]').forEach(function (workspace, workspaceIndex) {
    var tabs = Array.prototype.slice.call(workspace.querySelectorAll('[data-products-tab]'));
    var panels = Array.prototype.slice.call(workspace.querySelectorAll('[data-products-panel]'));
    if (!tabs.length || !panels.length) return;

    function navigates(tab) {
      var href = tab.getAttribute('href') || '';
      return href && href !== '#';
    }

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
        if (navigates(tab)) return;
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
        var nextTab = tabs[nextIndex];
        if (navigates(nextTab)) {
          nextTab.focus();
          return;
        }
        activate(nextTab.getAttribute('data-products-tab'), true);
      });
    });

    var selectedTab = workspace.querySelector('[data-products-tab][aria-selected="true"]') || tabs[0];
    activate(selectedTab.getAttribute('data-products-tab'), false);
  });
}

function initDashboardActiveTabScroll() {
  var activeTab = document.querySelector('.dash-tab[aria-current="page"], .dash-tab[aria-selected="true"]');
  if (!activeTab) return;

  var scroller = activeTab.closest('.merchant-dashboard-tab-scroll') || activeTab.closest('.overflow-x-auto') || activeTab.parentElement;
  if (!scroller) return;

  var isVertical = window.getComputedStyle(scroller).flexDirection === 'column';
  var hasOverflow = isVertical
    ? scroller.scrollHeight > scroller.clientHeight
    : scroller.scrollWidth > scroller.clientWidth;
  if (!hasOverflow) return;

  window.requestAnimationFrame(function () {
    if (isVertical) {
      var top = activeTab.offsetTop - ((scroller.clientHeight - activeTab.offsetHeight) / 2);
      scroller.scrollTo({ top: Math.max(0, top), behavior: 'auto' });
      return;
    }
    var left = activeTab.offsetLeft - ((scroller.clientWidth - activeTab.offsetWidth) / 2);
    scroller.scrollTo({ left: Math.max(0, left), behavior: 'auto' });
  });
}

function initMerchantWithdrawalBalance() {
  document.querySelectorAll('[data-withdrawal-form]').forEach(function (form) {
    var walletSelect = form.querySelector('[data-withdrawal-wallet]');
    var assetSelect = form.querySelector('[data-withdrawal-asset]');
    var amountInput = form.querySelector('[data-withdrawal-amount]');
    var chainInput = form.querySelector('[data-withdrawal-chain]');
    var symbolInput = form.querySelector('[data-withdrawal-symbol]');
    var tokenInput = form.querySelector('[data-withdrawal-token-address]');
    var availableDisplay = form.querySelector('[data-withdrawal-available-display]');
    var maxButton = form.querySelector('[data-withdrawal-max]');
    if (!walletSelect || !assetSelect || !amountInput || !availableDisplay || !maxButton) return;

    var balances = {};
    Array.prototype.slice.call(form.querySelectorAll('[data-withdrawal-balance]')).forEach(function (node) {
      var walletID = compactText(node.getAttribute('data-wallet-id') || '');
      var assetKey = normalizeAssetKey(node.getAttribute('data-asset-key') || '');
      if (!walletID || !assetKey) return;
      balances[walletID + '::' + assetKey] = {
        availableRaw: compactText(node.getAttribute('data-available-raw') || ''),
        availableInput: compactText(node.getAttribute('data-available-input') || ''),
        availableDisplay: compactText(node.getAttribute('data-available-display') || ''),
        symbol: compactText(node.getAttribute('data-symbol') || ''),
      };
    });

    function selectedOption(select) {
      if (!select || select.selectedIndex < 0) return null;
      return select.options[select.selectedIndex] || null;
    }

    function updateWithdrawalBalance() {
      var assetOption = selectedOption(assetSelect);
      var walletID = compactText(walletSelect.value || '');
      var assetKey = assetOption ? normalizeAssetKey(assetOption.getAttribute('data-balance-key') || '') : '';

      if (chainInput) chainInput.value = assetOption ? compactText(assetOption.getAttribute('data-chain') || '') : '';
      if (symbolInput) symbolInput.value = assetOption ? compactText(assetOption.getAttribute('data-symbol') || '') : '';
      if (tokenInput) tokenInput.value = assetOption ? compactText(assetOption.getAttribute('data-token-address') || '') : '';

      var balance = walletID && assetKey ? balances[walletID + '::' + assetKey] : null;
      if (!balance && assetOption && assetOption.value) {
        balance = {
          availableRaw: compactText(assetOption.getAttribute('data-available-raw') || ''),
          availableInput: compactText(assetOption.getAttribute('data-available-input') || ''),
          availableDisplay: compactText(assetOption.getAttribute('data-available-display') || ''),
          symbol: compactText(assetOption.getAttribute('data-symbol') || ''),
        };
      }
      maxButton.disabled = true;
      maxButton.removeAttribute('data-max-amount');
      maxButton.removeAttribute('data-max-raw');

      if (!walletID || !assetOption || !assetOption.value) {
        availableDisplay.textContent = 'Reserve cüzdan ve asset seçin';
        return;
      }
      if (!balance || !isPositiveIntegerString(balance.availableRaw)) {
        availableDisplay.textContent = 'Seçili asset için kullanılabilir bakiye yok';
        return;
      }

      availableDisplay.textContent = balance.availableDisplay || '0 ' + (balance.symbol || '');
      if (isPositiveIntegerString(balance.availableRaw) && isPositiveDecimalString(balance.availableInput)) {
        maxButton.disabled = false;
        maxButton.setAttribute('data-max-amount', balance.availableInput);
        maxButton.setAttribute('data-max-raw', balance.availableRaw);
      }
    }

    walletSelect.addEventListener('change', updateWithdrawalBalance);
    assetSelect.addEventListener('change', updateWithdrawalBalance);
    maxButton.addEventListener('click', function () {
      var maxAmount = maxButton.getAttribute('data-max-amount') || '';
      if (!maxAmount) return;
      amountInput.value = maxAmount;
      amountInput.dispatchEvent(new Event('input', { bubbles: true }));
      amountInput.dispatchEvent(new Event('change', { bubbles: true }));
      amountInput.focus();
    });

    updateWithdrawalBalance();
  });
}

function initPortalJWTProtection() {
  attachPortalJWTInputs();
  patchPortalJWTFetch();
}

function portalJWTCookie(name) {
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

function portalJWTToken() {
  return portalJWTCookie('gateway_portal_jwt');
}

function attachPortalJWTInputs() {
  var token = portalJWTToken();
  if (!token) return;
  document.querySelectorAll('form').forEach(function (form) {
    var method = (form.getAttribute('method') || 'get').toLowerCase();
    if (method !== 'post') return;
    var input = form.querySelector('input[name="_portal_jwt"]');
    if (!input) {
      input = document.createElement('input');
      input.type = 'hidden';
      input.name = '_portal_jwt';
      form.appendChild(input);
    }
    input.value = token;
  });
}

function patchPortalJWTFetch() {
  if (!window.fetch || window.fetch.__portalJWTPatched) return;
  var originalFetch = window.fetch;
  window.fetch = function (input, init) {
    init = init || {};
    var method = (init.method || (input && input.method) || 'GET').toUpperCase();
    if (method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS') {
      var url = typeof input === 'string' ? input : (input && input.url) || '';
      var sameOrigin = !url || url.indexOf('/') === 0 || url.indexOf(window.location.origin) === 0;
      var token = portalJWTToken();
      if (sameOrigin && token) {
        var headers = new Headers(init.headers || (input && input.headers) || {});
        if (!headers.has('X-Portal-JWT')) {
          headers.set('X-Portal-JWT', token);
        }
        init.headers = headers;
        if (!init.credentials) {
          init.credentials = 'same-origin';
        }
      }
    }
    return originalFetch(input, init);
  };
  window.fetch.__portalJWTPatched = true;
}

function initAdminRichSelects() {
  var controls = [];

  document.querySelectorAll('select[data-rich-select], .admin-vscode-body select, .merchant-workbench select, .merchant-modal-form select').forEach(function (select) {
    if (!shouldEnhanceSelect(select)) return;
    if (select.getAttribute('data-rich-select-ready') === 'true') return;
    select.setAttribute('data-rich-select-ready', 'true');

    var kind = select.getAttribute('data-rich-select') || inferRichSelectKind(select);
    var placeholder = select.getAttribute('data-placeholder') || selectPlaceholder(select);
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
    var menuPlacementActive = false;

    root.className = 'admin-rich-select';
    root.setAttribute('data-kind', kind);
    root.setAttribute('data-open', 'false');
    root.setAttribute('data-empty', 'true');
    if (isCompactSelect(select)) {
      root.setAttribute('data-size', 'compact');
    }

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
        if (option.disabled || option.hidden) return false;
        return option.value !== '' || !select.required;
      }).map(function (option) {
        return readOption(option);
      });
    }

    function selectedRecord() {
      var option = select.options[select.selectedIndex];
      if (!option || !option.value) return null;
      return readOption(option);
    }

    function currentPlaceholder() {
      return select.getAttribute('data-placeholder') || placeholder;
    }

    function updateTrigger() {
      var record = selectedRecord();
      var empty = !record;
      var placeholderText = currentPlaceholder();
      trigger.disabled = select.disabled;
      root.setAttribute('data-disabled', select.disabled ? 'true' : 'false');
      root.setAttribute('data-empty', empty ? 'true' : 'false');
      renderRichAvatar(avatar, empty ? { avatar: initials(placeholderText) } : record);
      primary.textContent = empty ? placeholderText : record.primary;
      meta.textContent = empty ? '' : record.meta;
      meta.hidden = empty || !record.meta;
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
        renderRichAvatar(optionAvatar, record);
        optionCopy.className = 'admin-rich-copy';
        optionPrimary.className = 'admin-rich-primary';
        optionPrimary.textContent = record.primary;
        optionMeta.className = 'admin-rich-meta';
        optionMeta.textContent = record.meta;
        optionMeta.hidden = !record.meta;
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
      var searchable = kind === 'wallet' || kind === 'asset' || optionRecords().length > 7;
      root.setAttribute('data-open', 'true');
      trigger.setAttribute('aria-expanded', 'true');
      portalMenu();
      menu.hidden = false;
      search.value = '';
      searchWrap.hidden = !searchable;
      renderOptions('');
      positionMenu();
      enableMenuPlacement();
      window.setTimeout(function () {
        positionMenu();
        var selected = optionsEl.querySelector('[aria-selected="true"]');
        if (selected) selected.scrollIntoView({ block: 'nearest' });
        if (searchable) {
          search.focus();
          return;
        }
        if (selected) {
          selected.focus();
          return;
        }
        focusFirstOption();
      }, 0);
    }

    function closeMenu() {
      root.setAttribute('data-open', 'false');
      trigger.setAttribute('aria-expanded', 'false');
      menu.hidden = true;
      disableMenuPlacement();
      resetMenuPlacement();
      restoreMenu();
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

    controls.push({ root: root, menu: menu, close: closeMenu });
    updateTrigger();
    updatePreview();

    function enableMenuPlacement() {
      if (menuPlacementActive) return;
      menuPlacementActive = true;
      window.addEventListener('resize', positionMenu);
      window.addEventListener('scroll', positionMenu, true);
    }

    function disableMenuPlacement() {
      if (!menuPlacementActive) return;
      menuPlacementActive = false;
      window.removeEventListener('resize', positionMenu);
      window.removeEventListener('scroll', positionMenu, true);
    }

    function resetMenuPlacement() {
      menu.removeAttribute('data-floating');
      [
        'position',
        'zIndex',
        'left',
        'right',
        'top',
        'bottom',
        'width',
        'maxHeight',
      ].forEach(function (name) {
        menu.style[name] = '';
      });
      optionsEl.style.maxHeight = '';
    }

    function portalMenu() {
      if (menu.parentNode !== document.body) {
        document.body.appendChild(menu);
      }
    }

    function restoreMenu() {
      if (menu.parentNode !== root) {
        root.appendChild(menu);
      }
    }

    function positionMenu() {
      if (menu.hidden) return;

      var viewportWidth = document.documentElement.clientWidth || window.innerWidth || 0;
      var viewportHeight = window.innerHeight || document.documentElement.clientHeight || 0;
      if (!viewportWidth || !viewportHeight) return;

      var padding = 12;
      var gap = 8;
      var rect = trigger.getBoundingClientRect();
      var availableWidth = Math.max(0, viewportWidth - (padding * 2));
      var width = Math.min(Math.max(rect.width, 260), availableWidth);
      var left = Math.min(Math.max(rect.left, padding), viewportWidth - width - padding);
      if (left < padding) left = padding;

      var spaceBelow = viewportHeight - rect.bottom - padding;
      var spaceAbove = rect.top - padding;
      var openAbove = spaceBelow < 220 && spaceAbove > spaceBelow;
      var availableHeight = Math.max(160, (openAbove ? spaceAbove : spaceBelow) - gap);

      menu.setAttribute('data-floating', 'true');
      menu.style.position = 'fixed';
      menu.style.zIndex = '240';
      menu.style.left = left + 'px';
      menu.style.right = 'auto';
      menu.style.bottom = 'auto';
      menu.style.width = width + 'px';
      menu.style.maxHeight = Math.min(360, availableHeight) + 'px';

      var searchHeight = searchWrap.hidden ? 0 : searchWrap.getBoundingClientRect().height;
      optionsEl.style.maxHeight = Math.max(96, Math.min(300, availableHeight - searchHeight - 8)) + 'px';

      var menuHeight = menu.getBoundingClientRect().height;
      var top = openAbove ? rect.top - gap - menuHeight : rect.bottom + gap;
      top = Math.min(Math.max(top, padding), viewportHeight - menuHeight - padding);
      menu.style.top = top + 'px';
    }

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
      if (!control.root.contains(event.target) && !control.menu.contains(event.target)) control.close();
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
  var chainSelect = document.getElementById('recover-chain-select');
  var assetSelect = form.querySelector('select[name="asset"]');
  var amountInput = document.getElementById('recover-amount-raw');
  var maxButton = document.getElementById('recover-max-button');
  var balanceDisplay = document.getElementById('recover-balance-display');
  var balanceRaw = document.getElementById('recover-balance-raw');
  var liveBalanceDisplay = document.getElementById('recover-live-balance-display');
  var liveBalanceRaw = document.getElementById('recover-live-balance-raw');
  var liveRefreshButton = document.getElementById('recover-live-refresh-button');
  var liveExplorerLink = document.getElementById('recover-live-explorer-link');
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
      lockedRaw: compactText(node.getAttribute('data-locked-raw') || '0'),
    };
  });

  var suppressRecoverAssetNavigate = false;

  function selectedAssetOption() {
    return assetSelect.options[assetSelect.selectedIndex] || null;
  }

  function selectedRecoverChainValue() {
    return chainSelect ? compactText(chainSelect.value || '') : '';
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

  function selectedAssetIsNative() {
    var option = selectedAssetOption();
    if (!option || !option.value) return false;
    return option.getAttribute('data-native') === 'true';
  }

  function recoverableRawForBalance(balance) {
    if (!balance) return '';
    if (isPositiveIntegerString(balance.availableRaw)) return balance.availableRaw;
    if (isPositiveIntegerString(balance.lockedRaw)) return balance.lockedRaw;
    return '';
  }

  function smallerPositiveIntegerString(left, right) {
    if (!isPositiveIntegerString(left)) return isPositiveIntegerString(right) ? right : '';
    if (!isPositiveIntegerString(right)) return left;
    return compareIntegerStrings(left, right) <= 0 ? left : right;
  }

  function notifyRecoverAssetSelectChanged() {
    suppressRecoverAssetNavigate = true;
    assetSelect.dispatchEvent(new Event('change', { bubbles: true }));
    suppressRecoverAssetNavigate = false;
  }

  function syncRecoverAssetOptions() {
    if (!chainSelect) return;

    var chainValue = selectedRecoverChainValue();
    var hasChain = !!chainValue;
    var selectedAllowed = false;
    var visibleCount = 0;

    assetSelect.disabled = !hasChain;
    Array.prototype.slice.call(assetSelect.options).forEach(function (option) {
      if (!option.value) {
        option.hidden = false;
        option.disabled = false;
        option.textContent = hasChain ? 'Asset seç' : 'Önce chain seç';
        return;
      }
      var allowed = hasChain && compactText(option.getAttribute('data-chain-id') || '') === chainValue;
      option.hidden = !allowed;
      option.disabled = !allowed;
      if (allowed) {
        visibleCount += 1;
        if (option.value === assetSelect.value) selectedAllowed = true;
      }
    });

    if (!selectedAllowed) {
      assetSelect.value = '';
    }
    assetSelect.setAttribute('data-placeholder', hasChain ? (visibleCount ? 'Asset seç' : 'Bu chain için asset yok') : 'Önce chain seç');
    notifyRecoverAssetSelectChanged();
  }

  function syncRecoverSourceWalletOptions() {
    var assetKey = selectedAssetKey();
    var hasAsset = !!assetKey;
    var selectedAllowed = false;
    var visibleCount = 0;

    walletSelect.disabled = !hasAsset;
    Array.prototype.slice.call(walletSelect.options).forEach(function (option) {
      if (!option.value) {
        option.hidden = false;
        option.disabled = false;
        return;
      }
      var balance = balances[option.value + '::' + assetKey];
      var allowed = hasAsset && balance && (isPositiveIntegerString(balance.availableRaw) || isPositiveIntegerString(balance.lockedRaw));
      option.hidden = !allowed;
      option.disabled = !allowed;
      if (allowed) {
        visibleCount += 1;
        if (option.value === walletSelect.value) selectedAllowed = true;
      }
    });

    if (!selectedAllowed) {
      walletSelect.value = '';
    }
    walletSelect.setAttribute('data-placeholder', hasAsset ? (visibleCount ? 'Bakiye olan source wallet seç' : 'Bu asset için bakiyeli wallet yok') : (chainSelect ? 'Önce chain ve asset seç' : 'Önce asset seç'));
    walletSelect.dispatchEvent(new Event('change', { bubbles: true }));
  }

  function recoverPathSelection() {
    var match = window.location.pathname.match(/^\/admin\/recover(?:\/([^/?#]+))?(?:\/([^/?#]+))?/);
    var decodePathPart = function (value) {
      try {
        return value ? decodeURIComponent(value) : '';
      } catch (err) {
        return value || '';
      }
    };
    return {
      chain: match && match[1] ? decodePathPart(match[1]) : '',
      asset: match && match[2] ? decodePathPart(match[2]) : '',
    };
  }

  function recoverAssetPathValue(assetValue) {
    assetValue = compactText(assetValue || '');
    if (!assetValue || assetValue.indexOf('|') < 0) return '';
    var parts = assetValue.split('|');
    var chain = compactText(parts.shift() || '');
    var identifier = compactText(parts.join('|') || '');
    if (!chain || !identifier) return '';
    return encodeURIComponent(chain) + '/' + encodeURIComponent(identifier);
  }

  function navigateToRecoverChain() {
    if (!chainSelect) return false;

    var chainValue = selectedRecoverChainValue();
    var base = compactText(chainSelect.getAttribute('data-recover-chain-url') || assetSelect.getAttribute('data-recover-asset-url') || '');
    if (!base) return false;

    var params = new URLSearchParams(window.location.search || '');
    var pathSelection = recoverPathSelection();
    var currentChain = pathSelection.chain || params.get('chain') || '';
    var currentAsset = params.get('asset') || '';
    var currentAssetChain = currentAsset.indexOf('|') > 0 ? currentAsset.split('|')[0] : '';
    if (chainValue && chainValue === currentChain) return false;
    if (currentAsset && chainValue === currentAssetChain) return false;

    var nextURL = new URL(base, window.location.origin);
    if (chainValue) {
      nextURL.pathname = nextURL.pathname.replace(/\/$/, '') + '/' + encodeURIComponent(chainValue);
    }
    var limit = params.get('limit') || '';
    if (limit) {
      nextURL.searchParams.set('limit', limit);
    }
    window.location.href = nextURL.pathname + nextURL.search;
    return true;
  }

  function navigateToRecoverAsset() {
    var option = selectedAssetOption();
    var assetValue = option && option.value ? option.value : '';
    var base = compactText(assetSelect.getAttribute('data-recover-asset-url') || '');
    if (!base) return false;

    var params = new URLSearchParams(window.location.search || '');
    var pathSelection = recoverPathSelection();
    var currentAsset = params.get('asset') || '';
    if (!currentAsset && assetValue && assetValue === (pathSelection.chain + '|' + pathSelection.asset)) return false;
    if (assetValue === currentAsset) return false;

    var nextURL = new URL(base, window.location.origin);
    if (assetValue) {
      var pathValue = recoverAssetPathValue(assetValue);
      if (pathValue) {
        nextURL.pathname = nextURL.pathname.replace(/\/$/, '') + '/' + pathValue;
      } else {
        nextURL.searchParams.set('asset', assetValue);
      }
    }
    var limit = params.get('limit') || '';
    if (limit) {
      nextURL.searchParams.set('limit', limit);
    }
    window.location.href = nextURL.pathname + nextURL.search;
    return true;
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
      lockedRaw: '0',
    };
    var label = balance.symbol || symbol || 'Asset';
    balanceDisplay.textContent = balance.available + ' ' + label;
    balanceRaw.textContent = 'Raw: ' + balance.availableRaw + (balance.locked ? ' · Locked: ' + balance.locked : '');

    if (isPositiveIntegerString(balance.availableRaw)) {
      if (selectedAssetIsNative()) {
        balanceState.textContent = 'Fee için Refresh gerekli';
        return;
      }
      balanceState.textContent = 'Kullanılabilir';
      maxButton.disabled = false;
      maxButton.setAttribute('data-max-raw', balance.availableRaw);
      return;
    }

    if (isPositiveIntegerString(balance.lockedRaw)) {
      if (selectedAssetIsNative()) {
        balanceState.textContent = 'Locked bakiye: fee için Refresh gerekli';
        return;
      }
      balanceState.textContent = 'Sweep-locked kullanılabilir';
      maxButton.disabled = false;
      maxButton.setAttribute('data-max-raw', balance.lockedRaw);
      return;
    }

    balanceState.textContent = 'Bakiye yok';
  }

  var liveBalanceRequestID = 0;

  function resetRecoverLiveBalance(display, raw) {
    liveBalanceDisplay.textContent = display;
    liveBalanceRaw.textContent = raw;
    if (liveExplorerLink) {
      liveExplorerLink.hidden = true;
      liveExplorerLink.removeAttribute('href');
    }
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
        var explorerURL = compactText(payload.explorer_url || '');
        var feeRaw = compactText(payload.network_fee_raw || '0');
        var feeError = compactText(payload.network_fee_error || '');
        var transferableRaw = compactText(payload.transferable_raw || '0');
        var feeText = isPositiveIntegerString(feeRaw) ? ' · Fee raw: ' + feeRaw : (feeError ? ' · Fee okunamadı' : '');
        var transferableText = isPositiveIntegerString(transferableRaw) ? ' · Net raw: ' + transferableRaw : '';
        resetRecoverLiveBalance('Canlı chain: ' + formatted + ' ' + symbol, 'Raw: ' + raw + feeText + transferableText + (address ? ' · ' + address : ''));
        if (liveExplorerLink && explorerURL) {
          liveExplorerLink.href = explorerURL;
          liveExplorerLink.hidden = false;
        }
        var assetKey = selectedAssetKey();
        var balance = balances[walletID + '::' + assetKey];
        var recoverableRaw = recoverableRawForBalance(balance);
        if (selectedAssetIsNative() && balance && isPositiveIntegerString(recoverableRaw)) {
          var grossMaxRaw = smallerPositiveIntegerString(recoverableRaw, raw);
          maxButton.disabled = true;
          maxButton.removeAttribute('data-max-raw');
          if (isPositiveIntegerString(feeRaw) && compareIntegerStrings(grossMaxRaw, feeRaw) > 0) {
            maxButton.disabled = false;
            maxButton.setAttribute('data-max-raw', grossMaxRaw);
            balanceState.textContent = isPositiveIntegerString(balance.availableRaw) ? 'Fee sonrası kullanılabilir' : 'Locked bakiye transfer edilebilir';
            balanceRaw.textContent = 'Raw: ' + balance.availableRaw + (balance.locked ? ' · Locked: ' + balance.locked : '') + ' · Max raw: ' + grossMaxRaw + ' · Fee raw: ' + feeRaw + ' · Net transfer raw: ' + subtractIntegerStrings(grossMaxRaw, feeRaw);
          } else {
            balanceState.textContent = 'Fee sonrası transfer yok';
          }
        }
        liveRefreshButton.disabled = false;
      })
      .catch(function (error) {
        if (requestID !== liveBalanceRequestID) return;
        resetRecoverLiveBalance('Canlı chain: okunamadı', compactText(error.message || 'Bilinmeyen hata'));
        liveRefreshButton.disabled = false;
      });
  }

  walletSelect.addEventListener('change', updateRecoverBalance);
  if (chainSelect) {
    chainSelect.addEventListener('change', function () {
      if (navigateToRecoverChain()) return;
      syncRecoverAssetOptions();
      syncRecoverSourceWalletOptions();
      updateRecoverBalance();
    });
  }
  assetSelect.addEventListener('change', function () {
    if (!suppressRecoverAssetNavigate && navigateToRecoverAsset()) return;
    syncRecoverSourceWalletOptions();
    updateRecoverBalance();
  });
  liveRefreshButton.addEventListener('click', fetchRecoverLiveBalance);
  maxButton.addEventListener('click', function () {
    var maxRaw = maxButton.getAttribute('data-max-raw') || '';
    if (!maxRaw) return;
    amountInput.value = maxRaw;
    amountInput.dispatchEvent(new Event('input', { bubbles: true }));
    amountInput.dispatchEvent(new Event('change', { bubbles: true }));
    amountInput.focus();
  });

  if (chainSelect) {
    syncRecoverAssetOptions();
  } else {
    syncRecoverSourceWalletOptions();
    updateRecoverBalance();
  }
}

function initAdminDataTables() {
  var tables = [];
  document.querySelectorAll('table[data-admin-table], .adm-wrap > table.adm-table').forEach(function (table) {
    if (tables.indexOf(table) === -1) tables.push(table);
  });

  tables.forEach(function (table, tableIndex) {
    if (table.getAttribute('data-admin-table-ready') === 'true') return;

    var isInteractiveTable = table.hasAttribute('data-admin-table');
    var tableID = compactText(table.id || '');
    if (!tableID) {
      tableID = 'admin-grid-' + (tableIndex + 1);
      table.id = tableID;
    }
    var tbody = table.tBodies[0];
    if (!tableID || !tbody) return;

    var rows = isInteractiveTable
      ? Array.prototype.slice.call(tbody.querySelectorAll('tr[data-admin-table-row]'))
      : Array.prototype.slice.call(tbody.querySelectorAll('tr')).filter(function (row) {
        return !row.hasAttribute('data-admin-table-empty') && !row.hasAttribute('data-admin-table-detail-for');
      });
    var emptyRow = tbody.querySelector('tr[data-admin-table-empty]');
    var detailRows = {};
    var searchInput = findAdminDataTableControl('data-admin-table-search', tableID);
    var countEl = findAdminDataTableControl('data-admin-table-count', tableID);
    var sortState = {
      column: -1,
      direction: 'none',
      type: 'text',
    };

    enhanceAdminGrid(table, tableID, rows, searchInput, countEl, isInteractiveTable);

    if (!isInteractiveTable) return;

    table.setAttribute('data-admin-table-ready', 'true');

    Array.prototype.slice.call(tbody.querySelectorAll('tr[data-admin-table-detail-for]')).forEach(function (row) {
      var key = row.getAttribute('data-admin-table-detail-for') || '';
      if (key) detailRows[key] = row;
    });

    rows.forEach(function (row, index) {
      row.setAttribute('data-admin-table-index', String(index));
      row.setAttribute('data-admin-row-expanded', 'false');
      row.setAttribute('data-admin-table-search-value', normalize((row.getAttribute('data-search') || '') + ' ' + row.textContent));
    });

    Array.prototype.slice.call(table.tHead ? table.tHead.querySelectorAll('th') : []).forEach(function (th, index) {
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
    emptyRow.hidden = visibleCount > 0;
  }

  updateAdminDataTableCount(countEl, visibleCount, rows.length, normalizedQuery !== '');
  updateAdminDataTableSortControls(table, sortState);
}

function enhanceAdminGrid(table, tableID, rows, searchInput, countEl, isInteractiveTable) {
  if (!table || table.getAttribute('data-admin-grid-ready') === 'true') return;
  table.setAttribute('data-admin-grid-ready', 'true');
  table.classList.add('kewl-grid-table');

  var wrap = table.closest('.adm-wrap');
  if (wrap) {
    wrap.classList.add('kewl-grid');
    wrap.setAttribute('data-grid-component', 'kewl-grid');
    wrap.setAttribute('data-grid-table', tableID);
    wrap.setAttribute('tabindex', '0');
    enhanceAdminGridPagination(wrap, tableID);
    enhanceAdminGridScrollState(wrap);
  }

  var toolbar = searchInput ? searchInput.closest('.admin-data-toolbar') : null;
  if (!toolbar && countEl) toolbar = countEl.closest('.admin-data-toolbar');
  if (toolbar) {
    toolbar.classList.add('kewl-grid-toolbar');
    toolbar.setAttribute('data-grid-toolbar', tableID);
    enhanceAdminGridToolbar(toolbar, tableID, table);
  }

  enhanceAdminGridHeaders(table);
  enhanceAdminGridRows(table, rows, isInteractiveTable);
}

function enhanceAdminGridPagination(wrap, tableID) {
  if (!wrap || wrap.getAttribute('data-grid-pagination-ready') === 'true') return;

  var pagerShell = null;
  var pager = null;
  var next = wrap.nextElementSibling;
  if (next) {
    if (next.classList && next.classList.contains('pg')) {
      pagerShell = next;
      pager = next;
    } else if (next.querySelector) {
      pager = next.querySelector('.pg');
      if (pager) pagerShell = next;
    }
  }

  if (!pagerShell || !pager) return;

  wrap.classList.add('has-grid-pagination');
  wrap.setAttribute('data-grid-pagination-ready', 'true');
  pagerShell.classList.add('kewl-grid-pagination');
  pagerShell.setAttribute('data-grid-pagination', tableID || '');
  pager.classList.add('kewl-grid-pagination-controls');
  if (!pager.getAttribute('aria-label')) {
    pager.setAttribute('aria-label', 'Sayfalama');
  }
}

function enhanceAdminGridToolbar(toolbar, tableID, table) {
  if (toolbar.getAttribute('data-grid-toolbar-ready') === 'true') return;
  toolbar.setAttribute('data-grid-toolbar-ready', 'true');
  toolbar.setAttribute('aria-label', table.getAttribute('data-grid-title') || 'Data grid araçları');
}

function enhanceAdminGridHeaders(table) {
  Array.prototype.slice.call(table.tHead ? table.tHead.querySelectorAll('th') : []).forEach(function (th, index) {
    if (!th.getAttribute('scope')) th.setAttribute('scope', 'col');
    th.setAttribute('data-grid-column-index', String(index));
    var sortButton = th.querySelector('.admin-sort-button[data-admin-sort]');
    var sortType = sortButton ? sortButton.getAttribute('data-admin-sort') : '';
    if (sortType) {
      th.setAttribute('data-grid-column-type', sortType);
      if (sortType === 'number') {
        th.classList.add('is-grid-numeric');
        markAdminGridColumnNumeric(table, index);
      }
    }
    if (!th.querySelector('.kewl-grid-resize-handle')) {
      var handle = document.createElement('span');
      handle.className = 'kewl-grid-resize-handle';
      handle.setAttribute('role', 'separator');
      handle.setAttribute('aria-orientation', 'vertical');
      handle.setAttribute('aria-hidden', 'true');
      handle.addEventListener('pointerdown', function (event) {
        startAdminGridColumnResize(event, table, th);
      });
      th.appendChild(handle);
    }
  });
}

function markAdminGridColumnNumeric(table, index) {
  Array.prototype.slice.call(table.tBodies || []).forEach(function (tbody) {
    Array.prototype.slice.call(tbody.rows || []).forEach(function (row) {
      if (row.hasAttribute('data-admin-table-detail-for') || row.hasAttribute('data-admin-table-empty')) return;
      var cell = row.cells[index];
      if (cell) cell.classList.add('is-grid-numeric');
    });
  });
}

function enhanceAdminGridScrollState(wrap) {
  if (!wrap || wrap.getAttribute('data-grid-scroll-ready') === 'true') return;
  wrap.setAttribute('data-grid-scroll-ready', 'true');

  var update = function () {
    var maxLeft = Math.max(0, wrap.scrollWidth - wrap.clientWidth);
    var maxTop = Math.max(0, wrap.scrollHeight - wrap.clientHeight);
    wrap.setAttribute('data-grid-can-scroll-x', maxLeft > 2 ? 'true' : 'false');
    wrap.setAttribute('data-grid-can-scroll-y', maxTop > 2 ? 'true' : 'false');
    wrap.setAttribute('data-grid-at-left', wrap.scrollLeft <= 1 ? 'true' : 'false');
    wrap.setAttribute('data-grid-at-right', wrap.scrollLeft >= maxLeft - 1 ? 'true' : 'false');
    wrap.setAttribute('data-grid-at-top', wrap.scrollTop <= 1 ? 'true' : 'false');
    wrap.setAttribute('data-grid-at-bottom', wrap.scrollTop >= maxTop - 1 ? 'true' : 'false');
  };

  var frame = 0;
  var scheduleUpdate = function () {
    if (frame) return;
    frame = requestAnimationFrame(function () {
      frame = 0;
      update();
    });
  };

  wrap.addEventListener('scroll', scheduleUpdate, { passive: true });
  window.addEventListener('resize', scheduleUpdate);
  var observer = null;
  if (typeof ResizeObserver === 'function') {
    observer = new ResizeObserver(scheduleUpdate);
    observer.observe(wrap);
    var table = wrap.querySelector(':scope > table');
    if (table) observer.observe(table);
    wrap._adminGridResizeObserver = observer;
  }
  wrap._adminGridCleanup = function () {
    wrap.removeEventListener('scroll', scheduleUpdate);
    window.removeEventListener('resize', scheduleUpdate);
    if (observer) observer.disconnect();
    if (frame) cancelAnimationFrame(frame);
    frame = 0;
  };
  scheduleUpdate();
}

function startAdminGridColumnResize(event, table, th) {
  if (event.button !== 0) return;
  event.preventDefault();
  event.stopPropagation();

  var startX = event.clientX;
  var startWidth = th.getBoundingClientRect().width;
  var minWidth = Number(th.getAttribute('data-grid-min-width') || 96);
  table.classList.add('is-resizing-column');

  function onMove(moveEvent) {
    var nextWidth = Math.max(minWidth, startWidth + moveEvent.clientX - startX);
    th.style.width = Math.round(nextWidth) + 'px';
  }

  function onUp() {
    table.classList.remove('is-resizing-column');
    document.removeEventListener('pointermove', onMove);
    document.removeEventListener('pointerup', onUp);
  }

  document.addEventListener('pointermove', onMove);
  document.addEventListener('pointerup', onUp);
}

function enhanceAdminGridRows(table, rows, isInteractiveTable) {
  rows.forEach(function (row, index) {
    row.classList.add('kewl-grid-row');
    row.setAttribute('data-grid-row-parity', index % 2 === 0 ? 'odd' : 'even');
    if (!isInteractiveTable) return;
    row.setAttribute('tabindex', index === 0 ? '0' : '-1');
    row.addEventListener('click', function () {
      selectAdminGridRow(table, row);
    });
    row.addEventListener('keydown', function (event) {
      handleAdminGridRowKeydown(event, table, row);
    });
  });
}

function selectAdminGridRow(table, row) {
  Array.prototype.slice.call(table.querySelectorAll('.kewl-grid-row[data-grid-selected="true"]')).forEach(function (selected) {
    selected.setAttribute('data-grid-selected', 'false');
    selected.setAttribute('tabindex', '-1');
  });
  row.setAttribute('data-grid-selected', 'true');
  row.setAttribute('tabindex', '0');
}

function handleAdminGridRowKeydown(event, table, row) {
  if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
  var visibleRows = Array.prototype.slice.call(table.querySelectorAll('.kewl-grid-row')).filter(function (candidate) {
    return !candidate.hidden;
  });
  var index = visibleRows.indexOf(row);
  var next = visibleRows[index + (event.key === 'ArrowDown' ? 1 : -1)];
  if (!next) return;
  event.preventDefault();
  selectAdminGridRow(table, next);
  next.focus({ preventScroll: true });
  next.scrollIntoView({ block: 'nearest', inline: 'nearest' });
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
  Array.prototype.slice.call(table.tHead ? table.tHead.querySelectorAll('th') : []).forEach(function (th, index) {
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
  var unit = compactText(countEl.getAttribute('data-label') || 'asset');
  var serverTotal = parseInt(countEl.getAttribute('data-total') || '', 10);
  var label = visibleCount + ' ' + unit;
  if (!filtered && Number.isFinite(serverTotal) && serverTotal > totalCount) {
    label = visibleCount + ' / ' + serverTotal + ' ' + unit;
  }
  if (filtered && totalCount !== visibleCount) {
    label = visibleCount + ' / ' + totalCount + ' ' + unit;
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

function subtractIntegerStrings(left, right) {
  var a = normalizeIntegerString(left);
  var b = normalizeIntegerString(right);
  if (compareIntegerStrings(a, b) <= 0) return '0';

  if (typeof BigInt === 'function') {
    try {
      return String(BigInt(a) - BigInt(b));
    } catch (error) {
      // Fall through to manual subtraction for environments without full BigInt parsing.
    }
  }

  var carry = 0;
  var out = '';
  var i = a.length - 1;
  var j = b.length - 1;
  for (; i >= 0; i -= 1, j -= 1) {
    var leftDigit = Number(a.charAt(i)) - carry;
    var rightDigit = j >= 0 ? Number(b.charAt(j)) : 0;
    if (leftDigit < rightDigit) {
      leftDigit += 10;
      carry = 1;
    } else {
      carry = 0;
    }
    out = String(leftDigit - rightDigit) + out;
  }
  out = out.replace(/^0+/, '');
  return out || '0';
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

function isPositiveDecimalString(value) {
  var raw = compactText(value);
  if (!/^(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)$/.test(raw)) return false;
  return raw.replace(/[.0]/g, '') !== '';
}

function readOption(option) {
  var text = compactText(option.textContent || '');
  var primary = option.getAttribute('data-primary') || text;
  var meta = option.getAttribute('data-meta') || '';
  var chip = option.getAttribute('data-chip') || '';
  var avatarSource = option.getAttribute('data-avatar') || primary;
  var logoURL = option.getAttribute('data-logo-url') || '';
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
    logoURL: compactText(logoURL),
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

function renderRichAvatar(node, record) {
  if (!node) return;
  node.textContent = '';
  var logoURL = record ? compactText(record.logoURL || '') : '';
  if (logoURL) {
    var img = document.createElement('img');
    img.src = logoURL;
    img.alt = '';
    img.loading = 'lazy';
    img.decoding = 'async';
    node.appendChild(img);
    return;
  }
  node.textContent = record && record.avatar ? record.avatar : '?';
}

function shouldEnhanceSelect(select) {
  if (!select || select.multiple || select.closest('.admin-rich-select')) return false;
  if (select.classList.contains('admin-native-select-hidden')) return false;
  if (select.getAttribute('data-native-select') === 'true') return false;
  if (select.getAttribute('data-rich-select-ready') === 'true') return true;
  var options = Array.prototype.slice.call(select.options || []);
  if (options.length === 0) return false;
  return Boolean(
    select.hasAttribute('data-rich-select') ||
    select.closest('.admin-vscode-body') ||
    select.closest('.merchant-workbench') ||
    select.closest('.merchant-modal-form')
  );
}

function inferRichSelectKind(select) {
  var key = normalize([select.name, select.id].join(' '));
  if (key.indexOf('wallet') !== -1) return 'wallet';
  if (key === 'asset' || key.indexOf(' asset') !== -1 || key.indexOf('symbol') !== -1) return 'asset';
  return 'default';
}

function isCompactSelect(select) {
  var className = select.getAttribute('class') || '';
  var key = normalize([select.name, select.id].join(' '));
  return className.indexOf('min-h-9') !== -1 ||
    key === 'status' ||
    key.indexOf('status') !== -1 ||
    key.indexOf('merchant_id') !== -1;
}

function selectPlaceholder(select) {
  var emptyOption = Array.prototype.slice.call(select.options || []).find(function (option) {
    return !option.value;
  });
  if (emptyOption && compactText(emptyOption.textContent)) {
    return compactText(emptyOption.textContent);
  }
  var label = select.closest('label');
  if (label) {
    var clone = label.cloneNode(true);
    Array.prototype.slice.call(clone.querySelectorAll('select, input, textarea, button, .admin-rich-select')).forEach(function (node) {
      node.remove();
    });
    var labelText = compactText(clone.textContent);
    if (labelText) return labelText;
  }
  return 'Seç';
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
