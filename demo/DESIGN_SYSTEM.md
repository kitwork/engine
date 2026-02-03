# 🎨 Kitwork Industrial Design System (v15.2)
> **The Sovereign Standard for Autonomous UI Agents**

This document serves as the **SINGLE SOURCE OF TRUTH** for the visual and architectural standards of the Kitwork Engine. Any agent or developer modifying the UI must adhere strictly to these protocols.

---

## 1. 🏗️ System Architecture

The design system is **NOT** a static CSS library. It is a **Just-In-Time (JIT) Generated Framework** powered by a sovereign Go engine.

*   **Engine Core**: `demo/css_jit_demo.go`
*   **Input Views**: `demo/view/*.html`
*   **Output Assets**:
    *   `demo/public/css/framework.css` (Base reset & common utilities)
    *   `demo/public/css/jit.css` (Dynamic utilities generated from HTML scan)

### 🔄 The Build Cycle
When you modify HTML class names, you **MUST** run the JIT engine to generate the corresponding CSS:
```bash
go run demo/css_jit_demo.go
```
*Failure to run this command will result in missing styles.*

---

## 2. 🧠 Design Philosophy: "Industrial Sovereignty"

We reject the "utility-soup" of modern web development in favor of **Explicit, Mechanical Precision**.

1.  **NO Aliases**: We do not use `mt-4` or `flex-col`. We speak plainly: `margin-top-16px`, `flex-direction-column`.
2.  **Explicit Units**: Every number must have a unit or meaning. `width-100` is invalid; `width-100pct` or `width-100px` is sovereign.
3.  **Negative Value Syntax**: Negative values always start with `-`. Example: `-translate-y-4px` (Move up 4px).
4.  **Atmospheric Depth**: The interface is not flat. It exists in a void (`background-black`) with layers of glass (`blur`), light (`glow`), and structure (`border-white-5`).

---

## 3. 🎨 Sovereign Color Palette

Usage Pattern: `{property}-{color}` or `{property}-{color}-{opacity}`

| Token | Hex | Role & Usage Rule |
| :--- | :--- | :--- |
| **`brand`** | `#f82244` | **The Pulse**. Use for primary actions, active states, and "hot" UI elements. |
| **`gold`** | `#eab308` | **The Architect**. Use for key data points, JSON keys, and warnings. |
| **`success`** | `#02D842` | **The Signal**. Use for "Operational" status, success metrics, and stability indicators. |
| **`black`** | `#000000` | **The Void**. Only for the root `<body>` and full-screen section backgrounds. |
| **`dark`** | `#080808` | **The Floor**. Slightly lighter than black. Use for alternation in sections. |
| **`elegant`** | `#0c0c0c` | **The Surface**. Use for Cards, Panels, and floating elements. |
| **`white`** | `#FFFFFF` | **The Light**. pure white for headings. Use opacities (`white-40`, `white-60`) for body text. |

**Opacity Modifiers:**
*   `text-white-40` (Secondary Text)
*   `border-white-10` (Subtle Dividers)
*   `background-brand-10` (Tinted Backgrounds)

---

## 4. 📐 Layout & Spatial System

### The Grid
We use a **12-Column Grid** by default.
*   **Container**: `.container` (Max-width 1280px, centered).
*   **Setup**: `display-grid grid-columns-12 gap-x-32px`.
*   **Spanning**: `grid-span-4`, `grid-span-12`.

### Responsive Strategy (Mobile-First Override)
We design for Desktop first, then override for Tablet/Mobile using prefixes.
*   `tablet:` (@media max-width: 900px)
*   `mobile:` (@media max-width: 600px)

**Example:**
```html
<div class="grid-span-4 tablet:grid-span-12 mobile:display-none">
    <!-- 4 cols on Desktop, Full width on Tablet, Hidden on Mobile -->
</div>
```

---

## 5. 🔠 Typography (The "Outfit" & "Mono")

### Font Families
*   **UI / Headings**: `font-outfit`
*   **Data / Code**: `font-mono` ("JetBrains Mono")

### The Scale (Explicit Pixels)
*   **Headings**: `font-size-72px`, `font-size-56px`, `font-size-40px`.
*   **Body**: `font-size-20px` (Large lead), `font-size-16px` (Standard), `font-size-14px` (Small).
*   **Micro**: `font-size-11px` (Labels, Badges).

### Letter Spacing
Industrial typography requires manual kerning adjustment.
*   **Large Headings**: `-letter-spacing-2px` or `-letter-spacing-4px` (Tight).
*   **Micro Labels**: `letter-spacing-2px` (Wide, Uppercase).

---

## 6. ✨ Industrial FX (The "Glow")

The "Sovereign" look is defined by **Lighting** and **Glass**.

*   **`shadow-glow-brand`**: Creates a localized red light source. Use on hover for active elements.
*   **`shadow-system`**: Creates heavy, physical depth. Use on cards.
*   **`blur-medium`**: Creates frosted glass. Use on Navbars (`background-black-30`).
*   **`animate-pulse`**: Creates a "breathing" status light.

---

## 7. 🧩 Component Recipes

### 7.1 The "Status Badge"
```html
<div class="display-inline-flex items-center gap-x-12px background-brand-10 text-brand font-black font-size-11px padding-y-10px padding-x-20px rounded-full border-1px border-brand-20 uppercase letter-spacing-2px">
    <span class="width-8px height-8px rounded-full background-brand shadow-glow"></span>
    System Operational
</div>
```

### 7.2 The "Code Visualizer" (Dark Mode)
```html
<div class="background-black border-top-4px border-top-gold padding-56px rounded-16px shadow-system">
    <code class="text-gold font-bold font-size-22px">{{ $variable }}</code>
    <h5 class="font-black font-size-26px margin-top-24px margin-bottom-16px letter-spacing-1px">Sovereign Layout</h5>
    <p class="text-white-40 line-height-170 font-size-15px">Description text here.</p>
</div>
```

### 7.3 The "Hero Button"
```html
<button class="background-brand text-white font-black font-size-13px uppercase padding-y-24px padding-x-64px rounded-4px border-none cursor-pointer transition-all hover:shadow-glow-brand hover:-translate-y-4px letter-spacing-2px">
    Verify Core
</button>
```

---

## 8. ⚠️ Troubleshooting / Common Mistakes

1.  **"My styles aren't showing up!"**
    *   **Fix**: Did you run `go run demo/css_jit_demo.go`? The CSS is JIT-generated. It doesn't exist until you run the engine.
2.  **"Using `flex` property incorrecty"**
    *   **Wrong**: `flex-1`
    *   **Right**: `flex-prop-1` or `flex-grow` (Check `css_jit_demo.go` regex for `flex-prop`).
3.  **"Typo overlapping"**
    *   **Fix**: Ensure `line-height` is set manually (e.g., `line-height-110`) for large text. Default line-height can be too loose.
4.  **"Z-Index issues with Haze/Grid layers"**
    *   **Fix**: Use negative z-indexes (`-z-index-1`, `-z-index-2`) for background atmospheric layers to keep them behind content.

---

**System Architect**: Huỳnh Nhân Quốc
**Last Updated**: v15.2
