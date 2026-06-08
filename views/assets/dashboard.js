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
