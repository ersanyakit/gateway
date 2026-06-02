document.addEventListener('DOMContentLoaded', function () {
  var tabs = Array.prototype.slice.call(document.querySelectorAll('[data-dashboard-tab]'));
  var panels = Array.prototype.slice.call(document.querySelectorAll('[data-dashboard-panel]'));
  if (tabs.length === 0 || panels.length === 0) {
    return;
  }

  function activate(tabName) {
    var exists = tabs.some(function (tab) {
      return tab.getAttribute('data-dashboard-tab') === tabName;
    });
    if (!exists) {
      tabName = tabs[0].getAttribute('data-dashboard-tab');
    }

    tabs.forEach(function (tab) {
      var selected = tab.getAttribute('data-dashboard-tab') === tabName;
      tab.setAttribute('aria-selected', selected ? 'true' : 'false');
    });

    panels.forEach(function (panel) {
      var active = panel.getAttribute('data-dashboard-panel') === tabName;
      panel.classList.toggle('hidden', !active);
      panel.setAttribute('aria-hidden', active ? 'false' : 'true');
    });
  }

  tabs.forEach(function (tab) {
    tab.addEventListener('click', function () {
      var tabName = tab.getAttribute('data-dashboard-tab');
      activate(tabName);
      if (history.replaceState) {
        history.replaceState(null, '', '#' + tabName);
      }
    });
  });

  window.addEventListener('hashchange', function () {
    activate(window.location.hash.replace('#', ''));
  });

  activate(window.location.hash.replace('#', '') || tabs[0].getAttribute('data-dashboard-tab'));
});
