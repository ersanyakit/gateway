document.addEventListener('DOMContentLoaded', function () {
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
      var original = button.innerHTML;
      button.innerText = 'Copied';
      window.setTimeout(function () {
        button.innerHTML = original;
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

  tick();
});
