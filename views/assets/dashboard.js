document.addEventListener('DOMContentLoaded', function () {
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
      button.innerText = 'Kopyalandı';
      window.setTimeout(function () {
        button.innerText = 'Kopyala';
      }, 1200);
    });
  });

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
