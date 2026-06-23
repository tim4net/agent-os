import '@testing-library/jest-dom'

// jsdom has no layout engine and does not implement Element.scrollIntoView.
// Stub it as a no-op so component tests that rely on auto-scroll (e.g. the Talk
// Mode transcript) don't throw at runtime.
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function scrollIntoView() {
    /* no-op in jsdom */
  }
}
