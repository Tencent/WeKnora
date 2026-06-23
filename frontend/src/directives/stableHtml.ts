import type { ObjectDirective } from 'vue';

function imageIdentity(img: HTMLImageElement): string {
  return (img.getAttribute('data-protected-src') || img.currentSrc || img.src || '').trim();
}

function canMorph(current: Node, next: Node): boolean {
  if (current.nodeType !== next.nodeType) return false;
  if (current.nodeType !== Node.ELEMENT_NODE) return true;
  return (current as Element).tagName === (next as Element).tagName;
}

function syncAttributes(current: Element, next: Element, preserved: Set<string> = new Set()): void {
  Array.from(current.attributes).forEach(({ name }) => {
    if (!preserved.has(name) && !next.hasAttribute(name)) current.removeAttribute(name);
  });
  Array.from(next.attributes).forEach(({ name, value }) => {
    if (!preserved.has(name) && current.getAttribute(name) !== value) {
      current.setAttribute(name, value);
    }
  });
}

function morphImage(current: HTMLImageElement, next: HTMLImageElement): void {
  const sameProtectedSource = Boolean(
    current.getAttribute('data-protected-src')
      && current.getAttribute('data-protected-src') === next.getAttribute('data-protected-src'),
  );
  const sameIdentity = imageIdentity(current) === imageIdentity(next);
  const sameProtectedAlt = Boolean(
    current.getAttribute('data-protected-src')
      && next.getAttribute('data-protected-src')
      && current.alt
      && current.alt === next.alt,
  );
  const keepDecodedImage = current.complete
    && current.naturalWidth > 1
    && (sameProtectedSource || sameIdentity || sameProtectedAlt);

  if (keepDecodedImage) {
    // Never write the placeholder src (or loading flags) over an image Chrome
    // has already decoded. More importantly, leave this exact DOM node attached
    // to the document so Chromium keeps its painted image layer.
    syncAttributes(current, next, new Set([
      'src',
      'data-protected-src',
      'data-img-loading',
      'data-auth-hydrated',
    ]));
    return;
  }

  syncAttributes(current, next);
}

function morphNode(current: Node, next: Node): void {
  if (current.nodeType === Node.TEXT_NODE || current.nodeType === Node.COMMENT_NODE) {
    if (current.nodeValue !== next.nodeValue) current.nodeValue = next.nodeValue;
    return;
  }

  const currentElement = current as Element;
  const nextElement = next as Element;
  if (currentElement instanceof HTMLImageElement && nextElement instanceof HTMLImageElement) {
    morphImage(currentElement, nextElement);
    return;
  }

  syncAttributes(currentElement, nextElement);
  morphChildren(currentElement, nextElement);
}

function morphChildren(currentParent: ParentNode, nextParent: ParentNode): void {
  const desiredChildren = Array.from(nextParent.childNodes);
  let current = currentParent.firstChild;

  for (const desired of desiredChildren) {
    if (current && canMorph(current, desired)) {
      const following = current.nextSibling;
      morphNode(current, desired);
      current = following;
      continue;
    }

    // Insert only the new/mismatched subtree. Existing matching ancestors and
    // their image descendants remain connected throughout streaming updates.
    currentParent.insertBefore(desired.cloneNode(true), current);
  }

  while (current) {
    const following = current.nextSibling;
    currentParent.removeChild(current);
    current = following;
  }
}

/**
 * Patch sanitized streaming HTML in place.
 *
 * Replacing innerHTML — or moving decoded images through a detached template —
 * makes Chromium briefly drop the image's painted layer on every typewriter
 * frame. This directive morphs text and surrounding markup without detaching a
 * matching loaded <img> node.
 */
export const vStableHtml: ObjectDirective<HTMLElement, string> = {
  beforeMount(el, binding) {
    el.innerHTML = binding.value || '';
  },
  updated(el, binding) {
    const html = binding.value || '';
    if (html === binding.oldValue) return;
    const template = document.createElement('template');
    template.innerHTML = html;
    morphChildren(el, template.content);
  },
};
