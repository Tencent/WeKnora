/**
 * Globally discourage browser/password-manager autofill on app inputs.
 * Skips elements that already declare autocomplete (e.g. login email).
 */
function guardElement(el: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement) {
  const explicit = el.getAttribute('autocomplete');
  if (explicit !== null && explicit !== '') return;
  if (el.closest('[data-allow-autofill]')) return;

  if (el instanceof HTMLInputElement && el.type === 'password') {
    el.setAttribute('autocomplete', 'new-password');
  } else {
    el.setAttribute('autocomplete', 'off');
  }
}

function guardTree(root: ParentNode) {
  if (root instanceof HTMLInputElement || root instanceof HTMLTextAreaElement || root instanceof HTMLSelectElement) {
    guardElement(root);
  }

  root.querySelectorAll?.('input, textarea, select').forEach((node) => {
    guardElement(node as HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement);
  });

  root.querySelectorAll?.('form').forEach((form) => {
    if (!form.hasAttribute('autocomplete')) {
      form.setAttribute('autocomplete', 'off');
    }
  });
}

let installed = false;

export function installAutofillGuard() {
  if (installed || typeof document === 'undefined') return;
  installed = true;

  guardTree(document);

  const observer = new MutationObserver((mutations) => {
    for (const mutation of mutations) {
      mutation.addedNodes.forEach((node) => {
        if (node instanceof HTMLElement) {
          guardTree(node);
        }
      });
    }
  });

  observer.observe(document.body, { childList: true, subtree: true });
}
