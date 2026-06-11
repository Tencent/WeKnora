/**
 * WeKnora embed widget — floating chat launcher.
 *
 * Usage:
 * <script src="https://your-weknora.example.com/weknora-widget.js"
 *         data-channel="CHANNEL_ID"
 *         data-token="em_..."
 *         data-position="bottom-right"
 *         data-primary-color="#0052d9"
 *         data-title="AI 客服"></script>
 */
(function () {
  'use strict';

  var script = document.currentScript;
  if (!script) return;

  var channelId = script.getAttribute('data-channel');
  var token = script.getAttribute('data-token');
  if (!channelId || !token) {
    console.warn('[WeKnora] data-channel and data-token are required');
    return;
  }

  var position = script.getAttribute('data-position') || 'bottom-right';
  var primaryColor = script.getAttribute('data-primary-color') || '#0052d9';
  var title = script.getAttribute('data-title') || 'AI Assistant';
  var base = script.src.replace(/\/weknora-widget\.js.*$/, '');
  var embedUrl = base + '/embed/' + encodeURIComponent(channelId) + '?token=' + encodeURIComponent(token);

  var panelOpen = false;

  var launcher = document.createElement('button');
  launcher.type = 'button';
  launcher.setAttribute('aria-label', title);
  launcher.textContent = '💬';
  launcher.style.cssText = [
    'position:fixed',
    'z-index:2147483000',
    'width:56px',
    'height:56px',
    'border-radius:50%',
    'border:none',
    'cursor:pointer',
    'font-size:24px',
    'box-shadow:0 4px 16px rgba(0,0,0,.18)',
    'background:' + primaryColor,
    'color:#fff',
    position.indexOf('left') >= 0 ? 'left:24px' : 'right:24px',
    position.indexOf('top') >= 0 ? 'top:24px' : 'bottom:24px',
  ].join(';');

  var panel = document.createElement('div');
  panel.style.cssText = [
    'position:fixed',
    'z-index:2147482999',
    'width:400px',
    'max-width:calc(100vw - 32px)',
    'height:600px',
    'max-height:calc(100vh - 100px)',
    'border-radius:12px',
    'overflow:hidden',
    'box-shadow:0 8px 32px rgba(0,0,0,.2)',
    'display:none',
    'background:#fff',
    position.indexOf('left') >= 0 ? 'left:24px' : 'right:24px',
    position.indexOf('top') >= 0 ? 'top:88px' : 'bottom:88px',
  ].join(';');

  var iframe = document.createElement('iframe');
  iframe.src = embedUrl;
  iframe.style.cssText = 'width:100%;height:100%;border:none';
  iframe.setAttribute('allow', 'clipboard-write');
  panel.appendChild(iframe);

  function toggle() {
    panelOpen = !panelOpen;
    panel.style.display = panelOpen ? 'block' : 'none';
    launcher.textContent = panelOpen ? '✕' : '💬';
  }

  launcher.addEventListener('click', toggle);
  document.body.appendChild(launcher);
  document.body.appendChild(panel);

  window.addEventListener('message', function (e) {
    if (!e.data || e.data.source !== 'weknora-embed') return;
    if (e.data.type === 'ready') {
      launcher.style.opacity = '1';
    }
  });
})();
