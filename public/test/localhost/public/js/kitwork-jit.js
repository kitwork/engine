/**
 * ⚡ KITWORK RUNTIME JIT (v1.0)
 * Sovereign Client-Side CSS Engine for SPA/Dynamic Content
 * 
 * Auto-generates CSS for utility classes found in the DOM.
 */

(function () {
    console.log("🚀 Kitwork Runtime JIT Initialized");

    // --- CONFIGURATION ---
    const Colors = {
        "white": "255, 255, 255", "black": "0, 0, 0", "brand": "248, 34, 68",
        "gold": "255, 217, 0", "success": "2, 216, 66", "dark": "18, 18, 18"
    };
    const MediaQueries = {
        "mobile": "@media (max-width: 600px)",
        "tablet": "@media (max-width: 900px)",
        "laptop": "@media (max-width: 1200px)",
        "desktop": "@media (min-width: 1280px)"
    };

    // --- STATE ---
    const seenClasses = new Set();
    const styleTag = document.createElement("style");
    styleTag.id = "kitwork-jit-runtime";
    document.head.appendChild(styleTag);

    // --- ENGINE ---
    function resolve(className) {
        if (seenClasses.has(className)) return;
        seenClasses.add(className);

        let core = className;
        let variants = [];

        // Parse Variants (e.g. tablet:hover:text-red)
        while (true) {
            let found = false;
            if (core.includes(":")) {
                const parts = core.split(":");
                const prefix = parts[0];
                if (MediaQueries[prefix] || ["hover", "focus", "active", "group-hover"].includes(prefix)) {
                    variants.push(prefix);
                    core = parts.slice(1).join(":");
                    found = true;
                }
            }
            if (!found) break;
        }

        // Parse Negative
        let neg = false;
        if (core.startsWith("-")) {
            neg = true;
            core = core.substring(1);
        }

        // Helper: Transform Unit (raw -> px/val)
        const toUnit = (val) => {
            if (!val) return "";
            if (val === "full") return "100%";
            if (val === "screen") return "100vh"; // Simplified
            if (/^\d+$/.test(val)) return val + "px"; // 12 -> 12px
            if (val.endsWith("pct")) return val.replace("pct", "%");
            return val;
        };

        let css = "";

        // --- REGISTRY PATTERNS (Simplified Port from Go) ---
        const patterns = [
            // Colors: text-brand, bg-black-50
            {
                reg: /^(text|background|border)-([a-z]+)(?:-(\d+))?$/, fn: (m) => {
                    let p = m[1] === "text" ? "color" : m[1] === "background" ? "background-color" : "border-color";
                    let c = Colors[m[2]];
                    if (!c) return "";
                    if (m[3]) return `${p}: rgba(${c}, ${parseInt(m[3]) / 100})`;
                    return `${p}: rgb(${c})`;
                }
            },
            // Spacing: margin-top-20px, padding-40px
            {
                reg: /^(margin|padding|gap)(?:-(top|bottom|left|right|x|y))?-([0-9a-z]+)$/, fn: (m) => {
                    let p = m[1], d = m[2], v = toUnit(m[3]);
                    if (neg) v = "-" + v;
                    if (!d) return `${p}: ${v}`;
                    if (d === "x") return `${p}-left: ${v}; ${p}-right: ${v}`;
                    if (d === "y") return `${p}-top: ${v}; ${p}-bottom: ${v}`;
                    return `${p}-${d}: ${v}`;
                }
            },
            // Sizing
            { reg: /^(width|height)-([0-9a-z]+)$/, fn: (m) => `${m[1]}: ${toUnit(m[2])}` },
            // Typography
            { reg: /^font-size-([0-9a-z]+)$/, fn: (m) => `font-size: ${toUnit(m[1])}` },
            { reg: /^font-(bold|medium|light|black)$/, fn: (m) => `font-weight: ${m[1] === "bold" ? 700 : m[1] === "black" ? 900 : 400}` },
            // Layout
            { reg: /^display-(block|flex|grid|none)$/, fn: (m) => `display: ${m[1]}` },
            { reg: /^grid-columns-(\d+)$/, fn: (m) => `grid-template-columns: repeat(${m[1]}, minmax(0, 1fr))` },
            // Misc
            { reg: /^cursor-(pointer|text)$/, fn: (m) => `cursor: ${m[1]}` }
        ];

        for (let p of patterns) {
            const m = core.match(p.reg);
            if (m) {
                css = p.fn(m);
                break;
            }
        }

        if (!css) return;

        // Wrap CSS
        let selector = "." + className.replace(/:/g, "\\:");
        if (variants.length > 0) {
            // Apply pseudo classes first
            variants.forEach(v => {
                if (!MediaQueries[v]) selector += ":" + v; // hover, focus
            });
            css = `${selector} { ${css} }`;

            // Apply Media Queries wrapper
            variants.forEach(v => {
                if (MediaQueries[v]) {
                    css = `${MediaQueries[v]} { ${css} }`;
                }
            });
        } else {
            css = `${selector} { ${css} }`;
        }

        // Inject
        styleTag.sheet.insertRule(css, styleTag.sheet.cssRules.length);
    }

    // --- OBSERVER ---
    function scan(node) {
        if (node.classList) {
            node.classList.forEach(resolve);
        }
        if (node.children) {
            for (let child of node.children) scan(child);
        }
    }

    // Initial Scan
    scan(document.body);

    // Watch for Changes
    new MutationObserver((mutations) => {
        mutations.forEach(m => {
            m.addedNodes.forEach(scan);
            if (m.type === "attributes" && m.attributeName === "class") {
                scan(m.target);
            }
        });
    }).observe(document.body, { childList: true, subtree: true, attributes: true });

})();
