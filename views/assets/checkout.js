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

    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(markCopied).catch(function () {});
      return;
    }

    var textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    document.body.appendChild(textarea);
    textarea.select();
    document.execCommand('copy');
    document.body.removeChild(textarea);
    markCopied();
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
    var pressChild = element.querySelector('.crypto-circle, .crypto-network-left img');
    element.classList.add('is-pressing');
    if (pressChild) {
      pressChild.setAttribute('data-press-style', pressChild.getAttribute('style') || '');
      pressChild.classList.add('is-pressing-child');
      pressChild.style.transform = 'scale(.88)';
      pressChild.style.filter = 'saturate(1.08)';
      if (pressChild.classList.contains('crypto-circle')) {
        pressChild.style.background = 'rgba(255, 55, 199, .12)';
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
