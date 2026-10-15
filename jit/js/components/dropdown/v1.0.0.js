/* dropdown component — something that opens and closes (KITWORK_UI §7, docs/components/
 * dropdown.md). It has no look of its own: what is written inside it is its interface, and the
 * behaviour is written in the markup, on the dropdown's host itself:
 *
 *   <box component="dropdown" as="$account" click-outside="close()" keydown:escape="close()">
 *     <button click="toggle()" expanded="{{ open }}">Quốc</button>
 *     <popover place="below-end">…</popover>
 *   </box>
 *
 * The engine writes data-kitwork-component="dropdown: $account", the events in data-kitwork-event,
 * aria-expanded in data-kitwork-bind, and the popover with data-kitwork-bind="show: open", so the
 * popover shows while the dropdown is open. Through the alias the rest of the page can open or close
 * it ($account.close()).
 *
 *   open       true while it is open
 *   toggle()   open when closed, close when open
 *   close()    close
 */
var dropdownDef = {
  open: false,

  toggle: function () { this.open = !this.open; },
  close: function () { this.open = false; }
};

window.kit.component("dropdown", dropdownDef);
