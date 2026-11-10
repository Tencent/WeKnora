export interface CaretCoordinates {
  top: number;
  left: number;
  height: number;
}

export function getCaretCoordinates(element: HTMLTextAreaElement, position: number): CaretCoordinates {
  const div = document.createElement('div');
  const style = window.getComputedStyle(element);
  
  // Copy styles
  const properties = [
    'direction', 'boxSizing', 'width', 'height', 'overflowX', 'overflowY',
    'borderTopWidth', 'borderRightWidth', 'borderBottomWidth', 'borderLeftWidth', 'borderStyle',
    'paddingTop', 'paddingRight', 'paddingBottom', 'paddingLeft',
    'fontStyle', 'fontVariant', 'fontWeight', 'fontStretch', 'fontSize', 'fontSizeAdjust', 'lineHeight', 'fontFamily',
    'textAlign', 'textTransform', 'textIndent', 'textDecoration', 'letterSpacing', 'wordSpacing',
    'tabSize', 'MozTabSize'
  ];

  properties.forEach(prop => {
    // @ts-ignore
    div.style[prop] = style[prop];
  });

  div.style.position = 'absolute';
  div.style.visibility = 'hidden';
  div.style.whiteSpace = 'pre-wrap';
  div.style.wordWrap = 'break-word';
  div.style.top = '0';
  div.style.left = '0';

  // We append a special character to the end of the text to handle the case where the caret is at the end
  const textContent = element.value.substring(0, position);
  div.textContent = textContent;
  
  const span = document.createElement('span');
  // Use a zero-width space to simulate the caret position without adding visible width, 
  // but if it's at the end of a line, we might need something else. 
  // Standard trick is using a pipe or similar and measuring it.
  span.textContent = '|'; 
  div.appendChild(span);
  
  document.body.appendChild(div);
  
  const spanRect = span.getBoundingClientRect();
  const divRect = div.getBoundingClientRect();
  
  const coordinates = {
    top: span.offsetTop + parseInt(style.borderTopWidth),
    left: span.offsetLeft + parseInt(style.borderLeftWidth),
    height: parseInt(style.lineHeight) || spanRect.height
  };
  
  document.body.removeChild(div);
  
  return coordinates;
}
