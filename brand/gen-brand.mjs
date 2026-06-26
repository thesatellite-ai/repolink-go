// repolink brand kit generator — single source of truth for the identity.
// Run: node gen-brand.mjs  → writes all SVG marks + design tokens here.
// Rasterize with the sibling Taskfile (`task raster`). Values mirror brand.json.
import { writeFileSync } from "node:fs"
import { dirname } from "node:path"
import { fileURLToPath } from "node:url"

const OUT = dirname(fileURLToPath(import.meta.url))

// ── Manifest (from brand.json) ───────────────────────────────────────────────
const M = {
  name: "repolink",
  tile: ["#8B5CF6", "#6D28D9"],
  fg: "#FFFFFF",
  accent: "#FBBF24",
  radius: 112,
  fonts: { display: "'Space Grotesk', 'Geist', ui-sans-serif, system-ui, sans-serif" },
  dark: ["#5B21B6", "#2E1065"], darkFg: "#EDE9FE",
  light: ["#F5F3FF", "#EDE9FE"], lightFg: "#6D28D9", lightAccent: "#D97706",
  glyphAccent: "#F59E0B",
}

// ── GLYPH: hub + links — one source node (accent) linked to consumer nodes ────
// orbit:true draws the connecting links; favicon drops them for 16px legibility.
function GLYPH(fg, accent, { orbit = true } = {}) {
  const links = orbit ? `<g stroke="${fg}" stroke-opacity="0.5" stroke-width="16" stroke-linecap="round">
    <line x1="256" y1="256" x2="256" y2="128"/>
    <line x1="256" y1="256" x2="136" y2="372"/>
    <line x1="256" y1="256" x2="376" y2="372"/>
  </g>\n  ` : ""
  return `${links}<g fill="${fg}">
    <rect x="224" y="96" width="64" height="64" rx="16"/>
    <rect x="104" y="340" width="64" height="64" rx="16"/>
    <rect x="344" y="340" width="64" height="64" rx="16"/>
  </g>
  <rect x="206" y="206" width="100" height="100" rx="26" fill="${accent}"/>`
}

// ── Generic machinery ────────────────────────────────────────────────────────
const grad = (id, a, b, x2 = 512, y2 = 512) =>
  `<linearGradient id="${id}" x1="0" y1="0" x2="${x2}" y2="${y2}" gradientUnits="userSpaceOnUse"><stop stop-color="${a}"/><stop offset="1" stop-color="${b}"/></linearGradient>`
const open = (w, h, label) =>
  `<svg width="${w}" height="${h}" viewBox="0 0 ${w} ${h}" fill="none" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="${label}">`
const W = (f, s) => writeFileSync(`${OUT}/${f}`, s)
const iconFile = (id, a, b, fg, accent, opts) =>
  `${open(512, 512, M.name)}
  <defs>${grad(id, a, b)}</defs>
  <rect width="512" height="512" rx="${M.radius}" fill="url(#${id})"/>
  ${GLYPH(fg, accent, opts)}
</svg>
`

const n = M.name
W(`${n}-icon.svg`,       iconFile("g-color", M.tile[0], M.tile[1], M.fg, M.accent))
W("icon.svg",            iconFile("g-c2",    M.tile[0], M.tile[1], M.fg, M.accent))
W(`${n}-icon-dark.svg`,  iconFile("g-dark",  M.dark[0], M.dark[1], M.darkFg, M.accent))
W(`${n}-icon-light.svg`, iconFile("g-light", M.light[0], M.light[1], M.lightFg, M.lightAccent))
W("favicon.svg",         iconFile("g-fav",   M.tile[0], M.tile[1], M.fg, M.accent, { orbit: false }))
W(`${n}-glyph.svg`,      `${open(512, 512, `${n} glyph`)}\n  ${GLYPH(M.lightFg, M.glyphAccent)}\n</svg>\n`)
W(`${n}-mono.svg`,       `${open(512, 512, n)}\n  ${GLYPH("currentColor", "currentColor")}\n</svg>\n`)

const FONT = M.fonts.display
W(`${n}-wordmark.svg`, `${open(360, 140, n)}\n  <text x="0" y="104" font-family="${FONT}" font-size="132" font-weight="600" letter-spacing="-6" fill="#0B0B12">${n}</text>\n</svg>\n`)

const lockup = (id, textFill) =>
  `${open(760, 200, n)}
  <defs>${grad(id, M.tile[0], M.tile[1])}</defs>
  <g transform="translate(20,36) scale(0.25)"><rect width="512" height="512" rx="${M.radius}" fill="url(#${id})"/>${GLYPH(M.fg, M.accent)}</g>
  <text x="176" y="132" font-family="${FONT}" font-size="116" font-weight="600" letter-spacing="-5" fill="${textFill}">${n}</text>
</svg>
`
W(`${n}-lockup.svg`,      lockup("l-color", "#0B0B12"))
W(`${n}-lockup-dark.svg`, lockup("l-dark", M.light[0]))

const TAGLINE = "One private repo, linked into every project."
const SUBLINE = "Local-first · open source · free."
const og = (file, bgA, bgB, main, sub1, sub2, line) =>
  W(file, `${open(1200, 630, n)}
  <defs>${grad("og-bg", bgA, bgB, 1200, 630)}${grad("og-ic", M.tile[0], M.tile[1])}</defs>
  <rect width="1200" height="630" fill="url(#og-bg)"/>
  <g transform="translate(96,180) scale(0.52)"><rect width="512" height="512" rx="${M.radius}" fill="url(#og-ic)"/>${GLYPH(M.fg, M.accent)}</g>
  <text x="412" y="292" font-family="${FONT}" font-size="120" font-weight="600" letter-spacing="-5" fill="${main}">${n}</text>
  <rect x="416" y="320" width="86" height="8" rx="4" fill="${line}"/>
  <text x="414" y="388" font-family="${FONT}" font-size="38" font-weight="500" fill="${sub1}">${TAGLINE}</text>
  <text x="414" y="440" font-family="${FONT}" font-size="28" font-weight="400" fill="${sub2}">${SUBLINE}</text>
</svg>
`)
og("og-cover.svg",       M.dark[0], M.dark[1], "#FFFFFF", M.light[1], "#C4B5FD", M.accent)
og("og-cover-light.svg", M.light[0], M.light[1], M.dark[1], M.dark[0], M.lightFg, M.lightFg)

W("tokens.css",
`:root{
  --${n}-tile-a:${M.tile[0]}; --${n}-tile-b:${M.tile[1]};
  --${n}-accent:${M.accent}; --${n}-fg:${M.fg};
  --${n}-font-display:${FONT};
  --${n}-font-sans:'Inter', ui-sans-serif, system-ui, sans-serif;
  --${n}-font-mono:'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, monospace;
  --${n}-radius:16px;
}
[data-theme="dark"], .dark{ --${n}-bg:${M.dark[1]}; --${n}-fg:${M.light[0]}; }
`)
W("palette.json", `${JSON.stringify({ name: n, tile: M.tile, accent: M.accent, fg: M.fg, dark: M.dark, light: M.light, glyphAccent: M.glyphAccent }, null, 2)}\n`)
W("tokens.json", `${JSON.stringify({ name: n, color: { tile: M.tile, accent: M.accent }, font: { display: "Space Grotesk", sans: "Inter", mono: "JetBrains Mono" }, radius: { tile: `${M.radius}@512` }, icon: { favicon: [16, 32, 48, 180, 512], og: [1200, 630] } }, null, 2)}\n`)

console.log(`✓ ${n} brand kit written to ${OUT}`)
