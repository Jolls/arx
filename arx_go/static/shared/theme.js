// Light/dark theme (#202). Loaded synchronously in <head> so data-bs-theme is
// set before first paint. Saved choice in localStorage as arx.theme
// ("light"/"dark"); absent = auto (follow the OS prefers-color-scheme).
(function() {
    var KEY = 'arx.theme';
    var mq = window.matchMedia('(prefers-color-scheme: dark)');
    var modes = {
        auto:  { icon: 'bi-circle-half', title: 'Theme: Auto' },
        light: { icon: 'bi-sun-fill',    title: 'Theme: Light' },
        dark:  { icon: 'bi-moon-fill',   title: 'Theme: Dark' }
    };

    function pref() {
        var v = null;
        try { v = localStorage.getItem(KEY); } catch (e) {}
        return v === 'light' || v === 'dark' ? v : 'auto';
    }

    function apply() {
        var p = pref();
        document.documentElement.setAttribute('data-bs-theme', p === 'auto' ? (mq.matches ? 'dark' : 'light') : p);
    }

    function syncButtons() {
        var m = modes[pref()];
        document.querySelectorAll('[data-theme-toggle]').forEach(function(btn) {
            btn.title = m.title;
            btn.setAttribute('aria-label', m.title);
            btn.innerHTML = '<i class="bi ' + m.icon + '"></i>';
        });
    }

    apply();
    mq.addEventListener('change', function() { if (pref() === 'auto') apply(); });

    document.addEventListener('DOMContentLoaded', function() {
        syncButtons();
        document.querySelectorAll('[data-theme-toggle]').forEach(function(btn) {
            btn.addEventListener('click', function() {
                var next = { auto: 'light', light: 'dark', dark: 'auto' }[pref()];
                try {
                    if (next === 'auto') localStorage.removeItem(KEY);
                    else localStorage.setItem(KEY, next);
                } catch (e) {}
                apply();
                syncButtons();
            });
        });
    });
})();
