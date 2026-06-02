document.addEventListener('DOMContentLoaded', function () {
  var countdown = document.querySelector('[data-countdown-unix]');
  if (!countdown) {
    return;
  }

  var expiresAt = Number(countdown.getAttribute('data-countdown-unix'));
  if (!Number.isFinite(expiresAt) || expiresAt <= 0) {
    return;
  }

  function tick() {
    var remaining = Math.max(0, expiresAt - Date.now());
    var totalSeconds = Math.floor(remaining / 1000);
    var minutes = Math.floor(totalSeconds / 60);
    var seconds = totalSeconds % 60;
    countdown.innerText = String(minutes).padStart(2, '0') + ':' + String(seconds).padStart(2, '0');
    if (remaining <= 0) {
      window.location.reload();
      return;
    }
    window.setTimeout(tick, 1000);
  }

  tick();
});
