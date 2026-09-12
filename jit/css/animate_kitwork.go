package css

// animate_kitwork.go — the Kitwork motion set: quiet, product-UI motion in the house design language.
//
// One distance (--animate-distance, 16px), one pace (--animate-duration, 0.5s), one curve (the
// spring, decelerating, no overshoot). Motion says four things and nothing else — where content
// comes from (enter), where it goes (exit), what just changed (attention: one beat) and what is
// running (live: loops). Nothing here shakes for effect; the theatrical vocabulary is animate.css's,
// vendored next door under its own names.
//
// Run-once entries read the shared vars and stick their final frame (fill:both); loops carry their
// own shorthand. `extra` is appended to the utility rule verbatim (a transform-origin, a gradient
// for shimmer). spin, ping, pulse and bounce are Tailwind's names and carry Tailwind's exact motion.
var animateKitwork = map[string]animateEntry{
	// ---- enter: content arrives ----------------------------------------------------------------
	"fade":     {group: "enter", frames: "@keyframes animate--fade{from{opacity:0}to{opacity:1}}"},
	"up":       {group: "enter", frames: "@keyframes animate--up{from{opacity:0;transform:translateY(var(--animate-distance))}to{opacity:1;transform:translateY(0)}}"},
	"down":     {group: "enter", frames: "@keyframes animate--down{from{opacity:0;transform:translateY(calc(-1 * var(--animate-distance)))}to{opacity:1;transform:translateY(0)}}"},
	"left":     {group: "enter", frames: "@keyframes animate--left{from{opacity:0;transform:translateX(calc(var(--animate-distance) + 4px))}to{opacity:1;transform:translateX(0)}}"},
	"right":    {group: "enter", frames: "@keyframes animate--right{from{opacity:0;transform:translateX(calc(-1 * var(--animate-distance) - 4px))}to{opacity:1;transform:translateX(0)}}"},
	"scale-in": {group: "enter", frames: "@keyframes animate--scale-in{from{opacity:0;transform:scale(0.96)}to{opacity:1;transform:scale(1)}}"},
	"rise":     {group: "enter", frames: "@keyframes animate--rise{from{opacity:0;transform:translateY(var(--animate-distance)) scale(0.98)}to{opacity:1;transform:translateY(0) scale(1)}}"},
	"flip-x":   {group: "enter", frames: "@keyframes animate--flip-x{from{opacity:0;transform:perspective(400px) rotateX(-80deg)}60%{opacity:1;transform:perspective(400px) rotateX(10deg)}80%{transform:perspective(400px) rotateX(-5deg)}to{transform:perspective(400px) rotateX(0deg)}}"},
	"flip-y":   {group: "enter", frames: "@keyframes animate--flip-y{from{opacity:0;transform:perspective(400px) rotateY(-80deg)}60%{opacity:1;transform:perspective(400px) rotateY(10deg)}80%{transform:perspective(400px) rotateY(-5deg)}to{transform:perspective(400px) rotateY(0deg)}}"},
	"reveal":   {group: "enter", frames: "@keyframes animate--reveal{from{clip-path:inset(0 100% 0 0)}to{clip-path:inset(0 0 0 0)}}"},

	// ---- exit: content leaves ------------------------------------------------------------------
	"vanish":    {group: "exit", frames: "@keyframes animate--vanish{from{opacity:1}to{opacity:0}}"},
	"out-up":    {group: "exit", frames: "@keyframes animate--out-up{from{opacity:1;transform:translateY(0)}to{opacity:0;transform:translateY(calc(-1 * var(--animate-distance)))}}"},
	"out-down":  {group: "exit", frames: "@keyframes animate--out-down{from{opacity:1;transform:translateY(0)}to{opacity:0;transform:translateY(var(--animate-distance))}}"},
	"out-left":  {group: "exit", frames: "@keyframes animate--out-left{from{opacity:1;transform:translateX(0)}to{opacity:0;transform:translateX(calc(-1 * var(--animate-distance) - 4px))}}"},
	"out-right": {group: "exit", frames: "@keyframes animate--out-right{from{opacity:1;transform:translateX(0)}to{opacity:0;transform:translateX(calc(var(--animate-distance) + 4px))}}"},
	"scale-out": {group: "exit", frames: "@keyframes animate--scale-out{from{opacity:1;transform:scale(1)}to{opacity:0;transform:scale(0.96)}}"},
	"sink":      {group: "exit", frames: "@keyframes animate--sink{from{opacity:1;transform:translateY(0) scale(1)}to{opacity:0;transform:translateY(var(--animate-distance)) scale(0.98)}}"},

	// ---- attention: one beat, look here --------------------------------------------------------
	"pop":       {group: "attention", frames: "@keyframes animate--pop{0%,100%{transform:scale(1)}40%{transform:scale(1.06)}}"},
	"nudge":     {group: "attention", frames: "@keyframes animate--nudge{0%,100%{transform:translateX(0)}20%{transform:translateX(-3px)}45%{transform:translateX(3px)}70%{transform:translateX(-2px)}}"},
	"tick":      {group: "attention", frames: "@keyframes animate--tick{0%,100%{transform:rotate(0)}25%{transform:rotate(-8deg)}60%{transform:rotate(8deg)}}", extra: "transform-origin:top center"},
	"highlight": {group: "attention", frames: "@keyframes animate--highlight{from{box-shadow:0 0 0 0 rgba(var(--color-brand),0.45)}to{box-shadow:0 0 0 12px rgba(var(--color-brand),0)}}", extra: "animation-fill-mode:none"},
	"heartbeat": {group: "attention", frames: "@keyframes animate--heartbeat{0%,100%{transform:scale(1)}14%{transform:scale(1.15)}28%{transform:scale(1)}42%{transform:scale(1.15)}70%{transform:scale(1)}}"},
	"rubber":    {group: "attention", frames: "@keyframes animate--rubber{0%{transform:scaleX(1) scaleY(1)}30%{transform:scaleX(1.3) scaleY(0.75)}40%{transform:scaleX(0.75) scaleY(1.25)}50%{transform:scaleX(1.15) scaleY(0.85)}65%{transform:scaleX(0.95) scaleY(1.05)}75%{transform:scaleX(1.05) scaleY(0.95)}to{transform:scaleX(1) scaleY(1)}}"},

	// ---- live: loops for what is running ------------------------------------------------------
	"spin":     {group: "live", loop: "animate--spin 1s linear infinite", frames: "@keyframes animate--spin{from{transform:rotate(0deg)}to{transform:rotate(360deg)}}"},
	"spin-ccw": {group: "live", loop: "animate--spin 1s linear infinite reverse"},
	"ping":     {group: "live", loop: "animate--ping 1s cubic-bezier(0,0,0.2,1) infinite", frames: "@keyframes animate--ping{75%,100%{transform:scale(2);opacity:0}}"},
	"pulse":    {group: "live", loop: "animate--pulse 2s cubic-bezier(0.4,0,0.6,1) infinite", frames: "@keyframes animate--pulse{50%{opacity:.5}}"},
	"bounce":   {group: "live", loop: "animate--bounce 1s infinite", frames: "@keyframes animate--bounce{0%,100%{transform:translateY(-25%);animation-timing-function:cubic-bezier(0.8,0,1,1)}50%{transform:none;animation-timing-function:cubic-bezier(0,0,0.2,1)}}"},
	"float":    {group: "live", loop: "animate--float 3.5s ease-in-out infinite", frames: "@keyframes animate--float{0%,100%{transform:translateY(0)}50%{transform:translateY(-8px)}}"},
	"blink":    {group: "live", loop: "animate--blink 1.2s step-start infinite", frames: "@keyframes animate--blink{0%,100%{opacity:1}50%{opacity:0}}"},
	"wave":     {group: "live", loop: "animate--wave 2.5s ease-in-out infinite", frames: "@keyframes animate--wave{0%{transform:rotate(0deg)}15%{transform:rotate(14deg)}30%{transform:rotate(-8deg)}40%{transform:rotate(14deg)}50%{transform:rotate(-4deg)}60%{transform:rotate(10deg)}70%{transform:rotate(0deg)}100%{transform:rotate(0deg)}}", extra: "transform-origin:70% 70%;display:inline-block"},
	"breathe":  {group: "live", loop: "animate--breathe 3s ease-in-out infinite", frames: "@keyframes animate--breathe{0%,100%{transform:scale(1)}50%{transform:scale(1.04)}}"},
	"shimmer":  {group: "live", loop: "animate--shimmer 1.6s linear infinite", frames: "@keyframes animate--shimmer{from{background-position:200% 0}to{background-position:-200% 0}}", extra: "background-image:linear-gradient(90deg,transparent 0%,rgba(var(--color-ink),0.06) 50%,transparent 100%);background-size:200% 100%;background-repeat:no-repeat"},
	"marquee":  {group: "live", loop: "animate--marquee 24s linear infinite", frames: "@keyframes animate--marquee{from{transform:translateX(0)}to{transform:translateX(-50%)}}"},
	"sweep":    {group: "live", loop: "animate--sweep 1.4s ease-in-out infinite", frames: "@keyframes animate--sweep{from{transform:translateX(-100%)}to{transform:translateX(300%)}}"},
	"drift":    {group: "live", loop: "animate--drift 30s ease-in-out infinite", frames: "@keyframes animate--drift{0%,100%{transform:translate(0,0) scale(1)}33%{transform:translate(5%,-4%) scale(1.06)}66%{transform:translate(-4%,5%) scale(0.96)}}"},
}

// animateKitworkGroups is the set in reading order — the shape of the gallery.
var animateKitworkGroups = []AnimationGroup{
	{Name: "enter", Animations: []string{"fade", "up", "down", "left", "right", "scale-in", "rise", "flip-x", "flip-y", "reveal"}},
	{Name: "exit", Animations: []string{"vanish", "out-up", "out-down", "out-left", "out-right", "scale-out", "sink"}},
	{Name: "attention", Animations: []string{"pop", "nudge", "tick", "highlight", "heartbeat", "rubber"}},
	{Name: "live", Animations: []string{"spin", "spin-ccw", "ping", "pulse", "bounce", "float", "blink", "wave", "breathe", "shimmer", "marquee", "sweep", "drift"}},
}
