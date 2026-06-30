/* =============================================================
   Arx — Price History chart (#284)
   Dependency-free SVG: unit cost (Y) over PO/price-list date (X),
   one point per record, hover tooltip. Renders window.PRICE_HISTORY
   into #price-history-chart, or call renderPriceHistory(el, points).
   ============================================================= */

(function () {
    const NS = 'http://www.w3.org/2000/svg';

    function svgEl(name, attrs) {
        const e = document.createElementNS(NS, name);
        for (const k in attrs) e.setAttribute(k, attrs[k]);
        return e;
    }

    function esc(s) {
        const d = document.createElement('div');
        d.textContent = s == null ? '' : String(s);
        return d.innerHTML;
    }

    function renderPriceHistory(container, points) {
        container.innerHTML = '';
        if (!points || !points.length) return;

        // Normalize + sort chronologically; drop unparseable dates.
        const pts = points
            .map(p => Object.assign({}, p, { t: Date.parse(p.date) }))
            .filter(p => !isNaN(p.t))
            .sort((a, b) => a.t - b.t);
        if (!pts.length) return;

        const W = 820, H = 360, padL = 60, padR = 20, padT = 20, padB = 44;
        const plotW = W - padL - padR, plotH = H - padT - padB;

        const ts = pts.map(p => p.t), cs = pts.map(p => p.cost);
        const minT = Math.min(...ts), maxT = Math.max(...ts), spanT = maxT - minT;

        let minC = Math.min(...cs), maxC = Math.max(...cs);
        if (minC === maxC) {
            const pad = Math.abs(minC) * 0.1 || 1;
            minC -= pad; maxC += pad;
        } else {
            const pad = (maxC - minC) * 0.1;
            minC -= pad; maxC += pad;
        }
        if (minC < 0 && Math.min(...cs) >= 0) minC = 0; // don't dip below 0 for non-negative costs
        const spanC = maxC - minC || 1;

        const xOf = t => spanT === 0 ? padL + plotW / 2 : padL + (t - minT) / spanT * plotW;
        const yOf = c => padT + (1 - (c - minC) / spanC) * plotH;

        const svg = svgEl('svg', {
            class: 'ph-svg', viewBox: `0 0 ${W} ${H}`, width: '100%',
            preserveAspectRatio: 'xMidYMid meet',
        });
        svg.style.maxWidth = W + 'px';

        // Y gridlines + cost labels.
        const yTicks = 5;
        for (let i = 0; i <= yTicks; i++) {
            const c = minC + spanC * i / yTicks;
            const y = yOf(c);
            svg.appendChild(svgEl('line', { class: 'ph-grid', x1: padL, y1: y, x2: W - padR, y2: y }));
            const lbl = svgEl('text', { class: 'ph-label', x: padL - 6, y: y + 3, 'text-anchor': 'end' });
            lbl.textContent = '$' + c.toFixed(2);
            svg.appendChild(lbl);
        }

        // X date labels (up to 5 evenly spaced ticks).
        const xTicks = Math.min(5, pts.length);
        for (let i = 0; i < xTicks; i++) {
            const t = (xTicks === 1 || spanT === 0)
                ? minT
                : minT + spanT * i / (xTicks - 1);
            const lbl = svgEl('text', { class: 'ph-label', x: xOf(t), y: H - padB + 18, 'text-anchor': 'middle' });
            lbl.textContent = new Date(t).toISOString().slice(0, 10);
            svg.appendChild(lbl);
        }

        // Axes.
        svg.appendChild(svgEl('line', { class: 'ph-axis', x1: padL, y1: padT, x2: padL, y2: H - padB }));
        svg.appendChild(svgEl('line', { class: 'ph-axis', x1: padL, y1: H - padB, x2: W - padR, y2: H - padB }));

        // Trend line through all points in date order.
        svg.appendChild(svgEl('polyline', {
            class: 'ph-line',
            points: pts.map(p => `${xOf(p.t)},${yOf(p.cost)}`).join(' '),
        }));

        // Tooltip element lives in the (position-relative) container.
        const tip = document.createElement('div');
        tip.className = 'ph-tooltip';
        container.appendChild(tip);

        // Points (drawn last so they sit above the line and catch hover).
        pts.forEach(p => {
            const cir = svgEl('circle', {
                class: 'ph-point ' + (p.source === 'price' ? 'ph-point-price' : 'ph-point-po'),
                cx: xOf(p.t), cy: yOf(p.cost), r: 4,
            });
            cir.addEventListener('mouseenter', () => {
                const head = p.source === 'price' ? 'Price list' : ('PO #' + (p.po || '—'));
                tip.innerHTML = `<strong>${esc(head)}</strong><br>${esc(p.supplier || '')}` +
                    `<br>${esc(p.date)} &bull; $${Number(p.cost).toFixed(2)}`;
                tip.style.display = 'block';
                const rect = svg.getBoundingClientRect();
                tip.style.left = (rect.width * xOf(p.t) / W) + 'px';
                tip.style.top = (rect.height * yOf(p.cost) / H) + 'px';
            });
            cir.addEventListener('mouseleave', () => { tip.style.display = 'none'; });
            svg.appendChild(cir);
        });

        container.insertBefore(svg, tip);
    }

    window.renderPriceHistory = renderPriceHistory;

    document.addEventListener('DOMContentLoaded', () => {
        const c = document.getElementById('price-history-chart');
        if (c && window.PRICE_HISTORY) renderPriceHistory(c, window.PRICE_HISTORY);
    });
})();
