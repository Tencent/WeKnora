/**
 * WeKnora embed widget SDK — floating chat launcher.
 *
 * Programmatic:
 *   WeKnora.init({ channel, token, position, primaryColor, title, baseUrl })
 *   WeKnora.open() | close() | toggle() | destroy()
 *   WeKnora.on('ready', fn) | off('ready', fn)
 *
 * Legacy script-tag auto-init via data-* attributes on the script element.
 */
(function (global) {
  'use strict';

  var HOST_SOURCE = 'weknora-host';
  var EMBED_SOURCE = 'weknora-embed';
  var POSITIONS = ['bottom-right', 'bottom-left', 'top-right', 'top-left'];
  var DEFAULT_POSITION = 'bottom-right';
  var DEFAULT_COLOR = '#0052d9';
  var DEFAULT_TITLE = 'AI Assistant';
  var DEFAULT_WIDTH = 400;
  var DEFAULT_HEIGHT = 600;

  var instance = null;
  var listeners = {};

  function normalizePosition(pos) {
    if (!pos || POSITIONS.indexOf(pos) < 0) return DEFAULT_POSITION;
    return pos;
  }

  function positionStyles(position, kind) {
    var isLeft = position.indexOf('left') >= 0;
    var isTop = position.indexOf('top') >= 0;
    var horizontal = isLeft ? 'left:24px' : 'right:24px';
    if (kind === 'launcher') {
      return horizontal + ';' + (isTop ? 'top:24px' : 'bottom:24px');
    }
    return horizontal + ';' + (isTop ? 'top:88px' : 'bottom:88px');
  }

  function emit(event, payload) {
    var handlers = listeners[event];
    if (!handlers) return;
    handlers.slice().forEach(function (fn) {
      try { fn(payload); } catch (e) { console.error('[WeKnora]', e); }
    });
  }

  function createWidget(opts) {
    var channelId = opts.channel || opts.channelId;
    var token = opts.token;
    if (!channelId || !token) {
      console.warn('[WeKnora] channel and token are required');
      return null;
    }

    var position = normalizePosition(opts.position);
    var primaryColor = opts.primaryColor || opts.primary_color || DEFAULT_COLOR;
    var title = opts.title || DEFAULT_TITLE;
    var baseUrl = (opts.baseUrl || opts.base || '').replace(/\/$/, '');
    if (!baseUrl) {
      var script = document.currentScript;
      if (script && script.src) {
        baseUrl = script.src.replace(/\/weknora-widget\.js.*$/, '');
      } else {
        baseUrl = global.location ? global.location.origin : '';
      }
    }

    var panelWidth = Number(opts.width) > 0 ? Number(opts.width) : DEFAULT_WIDTH;
    var panelHeight = Number(opts.height) > 0 ? Number(opts.height) : DEFAULT_HEIGHT;
    var embedOrigin = baseUrl;
    var embedUrl = baseUrl + '/embed/' + encodeURIComponent(channelId);
    var destroyed = false;
    var panelOpen = false;
    var iframeReady = false;
    var iframeOrigin = '';

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
      'opacity:0.92',
      'transition:opacity .2s',
      positionStyles(position, 'launcher'),
    ].join(';');

    var panel = document.createElement('div');
    panel.style.cssText = [
      'position:fixed',
      'z-index:2147482999',
      'width:' + panelWidth + 'px',
      'max-width:calc(100vw - 32px)',
      'height:' + panelHeight + 'px',
      'max-height:calc(100vh - 100px)',
      'border-radius:12px',
      'overflow:hidden',
      'box-shadow:0 8px 32px rgba(0,0,0,.2)',
      'display:none',
      'background:#fff',
      positionStyles(position, 'panel'),
    ].join(';');

    var iframe = document.createElement('iframe');
    iframe.src = embedUrl;
    iframe.style.cssText = 'width:100%;height:100%;border:none';
    iframe.setAttribute('allow', 'clipboard-write');
    iframe.setAttribute('title', title);
    panel.appendChild(iframe);

    function isTrustedOrigin(origin) {
      if (!origin || origin === 'null') return false;
      try {
        if (origin === embedOrigin) return true;
        if (iframeOrigin && origin === iframeOrigin) return true;
        return false;
      } catch (e) {
        return false;
      }
    }

    function postToIframe(message) {
      if (!iframe.contentWindow) return;
      var target = iframeOrigin || embedOrigin || '*';
      iframe.contentWindow.postMessage(message, target);
    }

    function provideToken() {
      postToIframe({
        source: HOST_SOURCE,
        type: 'provide_token',
        token: token,
        channel_id: channelId,
      });
    }

    function onMessage(e) {
      if (!e.data || e.data.source !== EMBED_SOURCE) return;
      if (e.data.channel_id && e.data.channel_id !== channelId) return;
      if (iframeOrigin && e.origin && !isTrustedOrigin(e.origin)) return;

      if (!iframeOrigin && e.origin) {
        iframeOrigin = e.origin;
      }

      switch (e.data.type) {
        case 'bootstrap_request':
          provideToken();
          break;
        case 'ready':
          iframeReady = true;
          launcher.style.opacity = '1';
          emit('ready', { channelId: channelId });
          break;
        case 'message_sent':
          emit('message_sent', {
            channelId: channelId,
            sessionId: e.data.session_id,
            query: e.data.query,
          });
          break;
        case 'message_received':
          emit('message_received', {
            channelId: channelId,
            sessionId: e.data.session_id,
            content: e.data.content,
          });
          break;
        default:
          break;
      }
    }

    function setOpen(next) {
      panelOpen = !!next;
      panel.style.display = panelOpen ? 'block' : 'none';
      launcher.textContent = panelOpen ? '✕' : '💬';
      if (panelOpen) {
        emit('open', { channelId: channelId });
      } else {
        emit('close', { channelId: channelId });
      }
    }

    function open() { setOpen(true); }
    function close() { setOpen(false); }
    function toggle() { setOpen(!panelOpen); }

    function destroy() {
      if (destroyed) return;
      destroyed = true;
      global.removeEventListener('message', onMessage);
      if (launcher.parentNode) launcher.parentNode.removeChild(launcher);
      if (panel.parentNode) panel.parentNode.removeChild(panel);
      listeners = {};
      if (instance === api) instance = null;
    }

    launcher.addEventListener('click', toggle);
    iframe.addEventListener('load', function () {
      try {
        if (iframe.contentWindow && iframe.contentWindow.location && iframe.contentWindow.location.origin) {
          iframeOrigin = iframe.contentWindow.location.origin;
        }
      } catch (err) {
        iframeOrigin = embedOrigin;
      }
      provideToken();
    });

    document.body.appendChild(launcher);
    document.body.appendChild(panel);
    global.addEventListener('message', onMessage);

    return {
      open: open,
      close: close,
      toggle: toggle,
      destroy: destroy,
      isOpen: function () { return panelOpen; },
      isReady: function () { return iframeReady; },
    };
  }

  var api = {
    init: function (opts) {
      if (instance) instance.destroy();
      instance = createWidget(opts || {});
      return instance;
    },
    open: function () { if (instance) instance.open(); },
    close: function () { if (instance) instance.close(); },
    toggle: function () { if (instance) instance.toggle(); },
    destroy: function () { if (instance) instance.destroy(); },
    on: function (event, handler) {
      if (!handler || typeof handler !== 'function') return;
      if (!listeners[event]) listeners[event] = [];
      listeners[event].push(handler);
    },
    off: function (event, handler) {
      if (!listeners[event]) return;
      if (!handler) {
        delete listeners[event];
        return;
      }
      listeners[event] = listeners[event].filter(function (fn) { return fn !== handler; });
    },
  };

  global.WeKnora = api;

  var legacyScript = document.currentScript;
  if (legacyScript) {
    var legacyChannel = legacyScript.getAttribute('data-channel');
    var legacyToken = legacyScript.getAttribute('data-token');
    if (legacyChannel && legacyToken) {
      api.init({
        channel: legacyChannel,
        token: legacyToken,
        position: legacyScript.getAttribute('data-position'),
        primaryColor: legacyScript.getAttribute('data-primary-color'),
        title: legacyScript.getAttribute('data-title'),
        baseUrl: legacyScript.getAttribute('data-base-url'),
        width: legacyScript.getAttribute('data-width'),
        height: legacyScript.getAttribute('data-height'),
      });
    }
  }
})(typeof window !== 'undefined' ? window : this);
