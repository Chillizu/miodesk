/* miodesk widget icons — small, single-color inline SVG set (no dependency).
   All icons render with stroke=currentColor; size controlled by CSS. */

"use strict";

const MIODESK_ICONS = {
  file: '<path d="M14 2.5H6.5A1.5 1.5 0 0 0 5 4v16a1.5 1.5 0 0 0 1.5 1.5h11A1.5 1.5 0 0 0 19 20V7.5z"/><path d="M14 2.5V7.5H19"/>',
  search: '<circle cx="10.5" cy="10.5" r="6.5"/><path d="M15.5 15.5 20 20"/>',
  folder: '<path d="M3 7a1.5 1.5 0 0 1 1.5-1.5H9l2 2h8.5A1.5 1.5 0 0 1 21 9v9a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 18z"/>',
  pencil: '<path d="M16.7 3.6a2 2 0 0 1 2.9 2.9L8 18.2 3.5 19.6l1.4-4.5z"/>',
  trash: '<path d="M4 6h16M9.5 6V4h5v2M18.5 6l-.9 13.2a1.5 1.5 0 0 1-1.5 1.3H7.9a1.5 1.5 0 0 1-1.5-1.3L5.5 6M10 10.5v6M14 10.5v6"/>',
  terminal: '<path d="M4.5 16.5l5.5-4.5-5.5-4.5M12.5 17.5h7"/>',
  play: '<path d="M8 5.5v13l11-6.5z"/>',
  clock: '<circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2.5"/>',
  x: '<circle cx="12" cy="12" r="8.5"/><path d="M9 9l6 6M15 9l-6 6"/>',
  gauge: '<path d="M3.5 12h4l2.5 7 4-14 2.5 7h4"/>',
  check: '<path d="M20 6.5 9.5 17 4 11.5"/>',
  alert: '<path d="M12 3.5 2.5 20h19z"/><path d="M12 9.5v5M12 17.2v.3"/>',
  copy: '<rect x="9" y="9" width="11" height="11" rx="1.5"/><path d="M5 15H4.5A1.5 1.5 0 0 1 3 13.5v-9A1.5 1.5 0 0 1 4.5 3h9A1.5 1.5 0 0 1 15 4.5V5"/>',
  dot: '<circle cx="12" cy="12" r="3.5" fill="currentColor" stroke="none"/>',
  link: '<path d="M10 14a4.5 4.5 0 0 0 6.4 0l3-3a4.5 4.5 0 0 0-6.4-6.4l-1.5 1.5"/><path d="M14 10a4.5 4.5 0 0 0-6.4 0l-3 3a4.5 4.5 0 0 0 6.4 6.4l1.5-1.5"/>',
};

function icon(name, cls) {
  const span = document.createElement("span");
  if (cls) span.className = cls || "ic";
  span.setAttribute("aria-hidden", "true");
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.7");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.innerHTML = MIODESK_ICONS[name] || MIODESK_ICONS.file;
  span.append(svg);
  return span;
}
