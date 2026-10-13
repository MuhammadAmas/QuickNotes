// Small dependency-free bar charts for the weekly summary and the users dashboard.
// Series colours come from CSS (--series-1, --series-2 in styles.css), validated for
// light and dark surfaces; categories are labelled on the axis, values in a hover tooltip
// and in a "Show as table" view.

const SVG = "http://www.w3.org/2000/svg";

function el(tag, attrs = {}, ...children) {
  const node = document.createElementNS(SVG, tag);
  for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, v);
  node.append(...children);
  return node;
}

function niceMax(v) {
  if (v <= 4) return 4;
  const pow = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * pow >= v) return m * pow;
  return 10 * pow;
}

/** A bar whose top corners are rounded (4px) and whose bottom sits flat on the baseline. */
function barPath(x, y, w, h, r = 4) {
  if (h <= 0) return "";
  const rr = Math.min(r, w / 2, h);
  return `M${x},${y + h}V${y + rr}Q${x},${y} ${x + rr},${y}H${x + w - rr}Q${x + w},${y} ${x + w},${y + rr}V${y + h}Z`;
}

/**
 * @param {object} o
 * @param {string[]} o.categories   x-axis labels
 * @param {{name: string, values: number[]}[]} o.series   1–2 series (colours by slot order)
 * @param {string} o.title          accessible name / table caption
 * @param {(v:number)=>string} [o.format]
 * @param {string[]} [o.axisLabels] shorter labels for the axis (e.g. only every 5th day); tooltips use `categories`
 * @param {number} [o.width] drawing width; use a smaller one for charts shown in narrow columns so text stays readable
 */
export function barChart({ categories, series, title, format = String, axisLabels = categories, width = 640 }) {
  const W = width, H = 220, pad = { top: 12, right: 8, bottom: 28, left: 32 };
  const plotW = W - pad.left - pad.right, plotH = H - pad.top - pad.bottom;
  const max = niceMax(Math.max(1, ...series.flatMap((s) => s.values)));
  const y = (v) => pad.top + plotH - (v / max) * plotH;
  const groupW = plotW / categories.length;
  const gap = 2; // surface gap between adjacent bars
  const barW = Math.max(4, Math.min(28, (groupW * 0.62 - gap * (series.length - 1)) / series.length));

  const root = document.createElement("figure");
  root.className = "viz";

  if (series.length > 1) {
    const legend = document.createElement("div");
    legend.className = "viz-legend";
    series.forEach((s, i) => {
      const item = document.createElement("span");
      item.innerHTML = `<i class="viz-swatch" style="background:var(--series-${i + 1})"></i>`;
      item.append(s.name);
      legend.append(item);
    });
    root.append(legend);
  }

  const svg = el("svg", { viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": title, class: "viz-svg" });
  // recessive grid + y labels
  for (const t of [0, max / 2, max]) {
    svg.append(el("line", { x1: pad.left, x2: W - pad.right, y1: y(t), y2: y(t), class: t === 0 ? "viz-base" : "viz-grid" }));
    svg.append(el("text", { x: pad.left - 6, y: y(t) + 4, "text-anchor": "end", class: "viz-tick" }, format(t)));
  }

  const tip = document.createElement("div");
  tip.className = "viz-tip";
  tip.hidden = true;

  categories.forEach((cat, ci) => {
    const gx = pad.left + ci * groupW;
    const total = series.length * barW + (series.length - 1) * gap;
    const x0 = gx + (groupW - total) / 2;
    const group = el("g", { class: "viz-group" });
    series.forEach((s, si) => {
      const v = s.values[ci] || 0;
      const d = barPath(x0 + si * (barW + gap), y(v), barW, y(0) - y(v));
      if (d) group.append(el("path", { d, fill: `var(--series-${si + 1})` }));
    });
    if (axisLabels[ci]) svg.append(el("text", { x: gx + groupW / 2, y: H - 8, "text-anchor": "middle", class: "viz-tick" }, axisLabels[ci]));
    // hit target: the whole column, bigger than the bars
    const hit = el("rect", { x: gx, y: pad.top, width: groupW, height: plotH, class: "viz-hit" });
    const show = () => {
      tip.replaceChildren();
      const head = document.createElement("b");
      head.textContent = cat;
      tip.append(head);
      series.forEach((s, si) => {
        const row = document.createElement("div");
        row.innerHTML = `<i class="viz-swatch" style="background:var(--series-${si + 1})"></i>`;
        row.append(`${s.name}: `, Object.assign(document.createElement("strong"), { textContent: format(s.values[ci] || 0) }));
        tip.append(row);
      });
      tip.hidden = false;
      const box = root.getBoundingClientRect();
      const sx = box.width / W;
      tip.style.left = `${Math.min(Math.max((gx + groupW / 2) * sx, 60), box.width - 60)}px`;
      group.classList.add("on");
    };
    const hide = () => { tip.hidden = true; group.classList.remove("on"); };
    hit.addEventListener("pointerenter", show);
    hit.addEventListener("pointerleave", hide);
    svg.append(group, hit);
  });

  root.append(svg, tip);

  // the same numbers as a table
  const details = document.createElement("details");
  details.className = "viz-table";
  details.innerHTML = "<summary>Show as table</summary>";
  const table = document.createElement("table");
  const headRow = document.createElement("tr");
  for (const th of ["", ...series.map((s) => s.name)]) headRow.append(Object.assign(document.createElement("th"), { textContent: th }));
  table.append(headRow);
  categories.forEach((cat, ci) => {
    const tr = document.createElement("tr");
    tr.append(Object.assign(document.createElement("th"), { textContent: cat }));
    for (const s of series) tr.append(Object.assign(document.createElement("td"), { textContent: format(s.values[ci] || 0) }));
    table.append(tr);
  });
  details.append(table);
  root.append(details);
  return root;
}
