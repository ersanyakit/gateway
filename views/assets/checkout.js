document.addEventListener('DOMContentLoaded', function () {
  document.body.classList.add('checkout-ready');

  var countdowns = Array.prototype.slice.call(document.querySelectorAll('[data-countdown-unix]'));
  var expiresAt = 0;

  countdowns.forEach(function (node) {
    var value = Number(node.getAttribute('data-countdown-unix'));
    if (Number.isFinite(value) && value > expiresAt) {
      expiresAt = value;
    }
  });

  function setCountdownText(node, text) {
    var target = node.querySelector('span') || node;
    target.innerText = text;
  }

  function tick() {
    if (!expiresAt) {
      return;
    }

    var remaining = Math.max(0, expiresAt - Date.now());
    var totalSeconds = Math.floor(remaining / 1000);
    var minutes = Math.floor(totalSeconds / 60);
    var seconds = totalSeconds % 60;
    var text = String(minutes).padStart(2, '0') + ':' + String(seconds).padStart(2, '0');

    countdowns.forEach(function (node) {
      if (node.hasAttribute('data-progress-fill')) {
        var elapsed = Math.max(0, 1800000 - remaining);
        var width = Math.min(100, Math.max(0, (elapsed / 1800000) * 100));
        node.style.width = width + '%';
        node.style.background = remaining > 120000 ? '#10b981' : remaining > 60000 ? '#f59e0b' : '#ef4444';
        return;
      }
      setCountdownText(node, text);
      if (remaining <= 0) {
        node.classList.add('is-expired');
      }
    });

    if (remaining <= 0) {
      window.location.reload();
      return;
    }
    window.setTimeout(tick, 1000);
  }

  function copyText(text, button) {
    function markCopied() {
      if (!button) {
        return;
      }
      var original = button.getAttribute('data-copy-original-html') || button.innerHTML;
      var copiedLabel = document.documentElement.lang === 'tr' ? 'Kopyalandı' : 'Copied';
      button.setAttribute('data-copy-original-html', original);
      button.classList.add('is-copied');
      button.innerText = copiedLabel;
      window.setTimeout(function () {
        button.innerHTML = original;
        button.classList.remove('is-copied');
      }, 1500);
    }

    function copyWithTextarea() {
      var textarea = document.createElement('textarea');
      textarea.value = text;
      textarea.setAttribute('readonly', '');
      textarea.style.position = 'fixed';
      textarea.style.opacity = '0';
      document.body.appendChild(textarea);
      textarea.focus();
      textarea.select();
      var copied = false;
      try {
        copied = document.execCommand('copy');
      } catch (err) {}
      document.body.removeChild(textarea);
      if (copied) {
        markCopied();
      }
    }

    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(markCopied).catch(copyWithTextarea);
      return;
    }

    copyWithTextarea();
  }

  document.querySelectorAll('[data-copy-target], [data-copy-value]').forEach(function (button) {
    button.addEventListener('click', function () {
      var value = button.getAttribute('data-copy-value');
      var targetID = button.getAttribute('data-copy-target');
      var target = targetID ? document.getElementById(targetID) : null;
      if (!value && target) {
        value = target.innerText;
      }
      if (value) {
        copyText(value, button);
      }
    });
  });

  function markPressing(element) {
    if (!element || element.classList.contains('is-disabled')) {
      return;
    }
    var pressChild = element.querySelector('.crypto-circle, .crypto-network-left img, .crypto-token-icon');
    element.classList.add('is-pressing');
    if (pressChild) {
      pressChild.setAttribute('data-press-style', pressChild.getAttribute('style') || '');
      pressChild.classList.add('is-pressing-child');
      pressChild.style.transform = 'scale(.88)';
      pressChild.style.filter = 'saturate(1.02)';
      if (pressChild.classList.contains('crypto-circle')) {
        pressChild.style.background = 'rgba(15, 23, 42, .06)';
        pressChild.style.boxShadow = 'inset 0 1px 3px rgba(13, 17, 28, .12)';
      }
    }
    window.setTimeout(function () {
      element.classList.remove('is-pressing');
      if (pressChild) {
        pressChild.classList.remove('is-pressing-child');
        var originalStyle = pressChild.getAttribute('data-press-style');
        if (originalStyle) {
          pressChild.setAttribute('style', originalStyle);
        } else {
          pressChild.removeAttribute('style');
        }
        pressChild.removeAttribute('data-press-style');
      }
    }, 180);
  }

  function isPlainNavigation(event, anchor) {
    return event.button === 0 &&
      !event.metaKey &&
      !event.ctrlKey &&
      !event.shiftKey &&
      !event.altKey &&
      !anchor.target &&
      !anchor.hasAttribute('download');
  }

  function navigateWithTransition(event, link, delay) {
    if (!isPlainNavigation(event, link)) {
      return;
    }
    var href = link.href;
    if (!href) {
      return;
    }
    event.preventDefault();
    document.body.classList.add('checkout-navigating');
    window.setTimeout(function () {
      window.location.assign(href);
    }, delay || 120);
  }

  document.querySelectorAll('.crypto-lang-option, .crypto-header-left[href], a.crypto-ghost-btn[href], a.crypto-copy-btn[href]').forEach(function (link) {
    link.addEventListener('click', function (event) {
      navigateWithTransition(event, link, 120);
    });
  });

  function setupAssetPicker() {
    var picker = document.querySelector('[data-asset-picker]');
    if (!picker) {
      return;
    }

    var tokenButtons = Array.prototype.slice.call(picker.querySelectorAll('[data-symbol].crypto-token-option'));
    var networkRows = Array.prototype.slice.call(picker.querySelectorAll('[data-network-row]'));
    var selectedTokenTitle = document.querySelector('[data-selected-token-title]');
    var selectedTokenLabel = picker.querySelector('[data-selected-token-label]');
    var networkPanel = picker.querySelector('[data-network-panel]');
    var networkPrompt = picker.querySelector('[data-network-prompt]');
    var emptyState = picker.querySelector('[data-network-empty]');
    var tokenSearch = picker.querySelector('[data-token-search]');
    var clearTokenButton = picker.querySelector('[data-clear-token]');
    var tokenStageTitle = picker.querySelector('[data-token-stage-title]');
    var isTurkish = document.documentElement.lang === 'tr';
    var defaultTokenTitle = isTurkish ? 'Coin seç' : 'Choose coin';
    var defaultNetworkLabel = isTurkish ? 'Coin bekleniyor' : 'Waiting for coin';
    var selectNetworkTitle = isTurkish ? 'Ağ seç' : 'Select network';

    function normalized(value) {
      return String(value || '').trim().toUpperCase();
    }

    function availableSymbol(symbol) {
      return tokenButtons.some(function (button) {
        return normalized(button.getAttribute('data-symbol')) === symbol;
      });
    }

    function updateTokenFilter() {
      var query = String(tokenSearch && tokenSearch.value || '').trim().toLowerCase();
      tokenButtons.forEach(function (button) {
        var haystack = String(button.getAttribute('data-token-search-text') || button.textContent || '').toLowerCase();
        button.hidden = query !== '' && haystack.indexOf(query) === -1;
      });
    }

    function clearSelection() {
      tokenButtons.forEach(function (button) {
        button.classList.remove('is-active');
        button.setAttribute('aria-selected', 'false');
      });

      networkRows.forEach(function (row) {
        row.hidden = true;
      });

      if (emptyState) {
        emptyState.hidden = true;
      }
      if (networkPrompt) {
        networkPrompt.hidden = false;
      }
      if (networkPanel) {
        networkPanel.classList.remove('has-token');
      }
      if (selectedTokenTitle) {
        selectedTokenTitle.textContent = defaultTokenTitle;
        selectedTokenTitle.hidden = true;
      }
      if (selectedTokenLabel) {
        selectedTokenLabel.textContent = defaultNetworkLabel;
      }
      if (tokenStageTitle) {
        tokenStageTitle.textContent = defaultTokenTitle;
      }
      if (clearTokenButton) {
        clearTokenButton.hidden = true;
      }
      if (tokenSearch) {
        tokenSearch.value = '';
        updateTokenFilter();
      }

      picker.classList.remove('has-token');
      picker.setAttribute('data-selected-symbol', '');
    }

    function selectToken(symbol) {
      symbol = normalized(symbol);
      if (!symbol || !availableSymbol(symbol)) {
        clearSelection();
        return;
      }

      var activeButton = null;
      tokenButtons.forEach(function (button) {
        var active = normalized(button.getAttribute('data-symbol')) === symbol;
        button.classList.toggle('is-active', active);
        button.setAttribute('aria-selected', active ? 'true' : 'false');
        if (active) {
          activeButton = button;
        }
      });

      var visibleRows = 0;
      networkRows.forEach(function (row) {
        var visible = normalized(row.getAttribute('data-symbol')) === symbol;
        row.hidden = !visible;
        if (visible) {
          visibleRows++;
        }
      });

      if (networkPrompt) {
        networkPrompt.hidden = true;
      }
      if (networkPanel) {
        networkPanel.classList.add('has-token');
      }

      if (emptyState) {
        emptyState.hidden = visibleRows > 0;
      }
      if (selectedTokenTitle && symbol) {
        selectedTokenTitle.hidden = true;
      }
      if (selectedTokenLabel) {
        selectedTokenLabel.textContent = selectNetworkTitle;
      }
      if (tokenStageTitle) {
        tokenStageTitle.textContent = symbol;
      }
      if (clearTokenButton) {
        clearTokenButton.hidden = false;
      }
      picker.classList.add('has-token');
      picker.setAttribute('data-selected-symbol', symbol);

      if (networkPanel && networkPanel.scrollIntoView) {
        networkPanel.scrollIntoView({ block: 'nearest', inline: 'nearest' });
      } else if (activeButton && activeButton.scrollIntoView) {
        activeButton.scrollIntoView({ block: 'nearest', inline: 'nearest' });
      }
    }

    tokenButtons.forEach(function (button) {
      button.addEventListener('pointerdown', function () {
        markPressing(button);
      });
      button.addEventListener('click', function () {
        if (tokenSearch) {
          tokenSearch.value = '';
          updateTokenFilter();
        }
        selectToken(button.getAttribute('data-symbol'));
      });
    });

    if (tokenSearch) {
      tokenSearch.addEventListener('input', updateTokenFilter);
    }
    if (clearTokenButton) {
      clearTokenButton.addEventListener('click', function () {
        clearSelection();
        if (tokenSearch) {
          tokenSearch.focus();
        }
      });
    }

    var querySymbol = '';
    try {
      querySymbol = new URLSearchParams(window.location.search).get('asset') || '';
    } catch (err) {}
    var initialSymbol = picker.getAttribute('data-selected-symbol') || querySymbol;
    if (!initialSymbol && tokenButtons.length === 1) {
      initialSymbol = tokenButtons[0].getAttribute('data-symbol');
    }
    selectToken(initialSymbol);
  }

  setupAssetPicker();

  document.querySelectorAll('.crypto-flow-select .crypto-btn').forEach(function (link) {
    link.addEventListener('pointerdown', function () {
      markPressing(link);
    });

    link.addEventListener('click', function (event) {
      link.classList.add('is-pressing');
      navigateWithTransition(event, link, 120);
    });
  });

  document.querySelectorAll('.crypto-flow-select .crypto-network-option').forEach(function (button) {
    button.addEventListener('pointerdown', function () {
      if (!button.disabled) {
        markPressing(button);
      }
    });

    button.addEventListener('click', function (event) {
      if (button.disabled || button.dataset.pressSubmit === '1') {
        return;
      }
      var form = button.form;
      if (!form) {
        return;
      }
      event.preventDefault();
      button.dataset.pressSubmit = '1';
      button.classList.add('is-pressing');
      document.body.classList.add('checkout-navigating');
      window.setTimeout(function () {
        if (form.requestSubmit) {
          form.requestSubmit(button);
          return;
        }
        form.submit();
      }, 105);
    });
  });

  tick();
});
