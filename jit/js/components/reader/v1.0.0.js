/* reader component — what a screen reader says aloud (KITWORK_UI §13, the built-in <reader>
 * of Kitwork UI). The engine writes it from <reader /> (or <reader as="$reader" />) as an invisible
 * live region:
 *
 *   <div data-kitwork-component="reader: $reader" data-kitwork-bind="text: text"
 *        aria-live="polite" aria-atomic="true" style="position:absolute;width:1px;…"></div>
 *
 * A move without a reload does not tell a screen reader that the page changed, so Drive says the new
 * page's title through kit.reader after every move. A page can say a sentence of its own the same way,
 * through the alias: click="$cart.add(product, 1); $reader.say('Đã thêm vào giỏ')".
 *
 * Markup never calls kit: kit.reader holds the sentence, this component is how markup hears it. Its
 * text is a getter that reads the service, so every reader on a page agrees.
 *
 *   text       the sentence being said ("" between two)
 *   say(text)  say a sentence; the same sentence twice is said twice
 */
var readerDef = {
  get text() { return window.kit.reader.text; },

  say: function (text) { window.kit.reader.say(text); }
};

window.kit.component("reader", readerDef);
