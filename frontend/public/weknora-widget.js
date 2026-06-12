/**
 * WeKnora embed widget SDK — floating chat launcher.
 *
 * Programmatic:
 *   WeKnora.init({ channel, token, position, primaryColor, title, baseUrl })
 *   WeKnora.open() | close() | toggle() | destroy()
 *   WeKnora.on('ready', fn) | off('ready', fn)
 *
 * Host context & actions (postMessage to iframe):
 *   WeKnora.setContext({ userId, page, ... })
 *     Inject visitor/page context merged into each chat query.
 *   WeKnora.openWithQuery('How do I reset my password?')
 *     Opens the panel (if closed) and sends the query when the iframe is ready.
 *   WeKnora.setLocale('en-US')
 *     Switch embed UI language (zh-CN | en-US | ko-KR | ru-RU).
 *
 * Secure mode (recommended): instead of `token`, pass `tokenEndpoint` — a URL on
 * your own backend that returns { token: "ems_...", expiresIn: 1800 }. Your
 * backend mints that short-lived session token by exchanging the publish token
 * (kept server-side) against POST /api/v1/embed/:channel/exchange. The publish
 * token then never reaches the browser; the widget auto-refreshes before expiry.
 *
 * Legacy script-tag auto-init via data-* attributes on the script element
 * (data-channel + data-token, or data-channel + data-token-endpoint).
 */
(function (global) {
  'use strict';

  var HOST_SOURCE = 'weknora-host';
  var EMBED_SOURCE = 'weknora-embed';
  var POSITIONS = ['bottom-right', 'bottom-left', 'top-right', 'top-left'];
  var DEFAULT_POSITION = 'bottom-right';
  var DEFAULT_COLOR = '#07C05F';
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
    // Insecure mode: a long-lived publish token is embedded in the page.
    var staticToken = opts.token;
    // Secure mode: the page never holds the publish token. Instead it points at
    // an endpoint on the integrator's own backend that mints a short-lived
    // session token (by server-side exchange of the publish token). The widget
    // fetches a fresh token here and refreshes it before expiry.
    var tokenEndpoint = opts.tokenEndpoint || opts.token_endpoint || '';
    if (!channelId || (!staticToken && !tokenEndpoint)) {
      console.warn('[WeKnora] channel and (token or tokenEndpoint) are required');
      return null;
    }

    var currentToken = staticToken || '';
    var tokenInFlight = null;
    var refreshTimer = null;

    function scheduleRefresh(expiresInSec) {
      if (!tokenEndpoint) return;
      if (refreshTimer) clearTimeout(refreshTimer);
      var ttl = Number(expiresInSec) > 0 ? Number(expiresInSec) : 1800;
      // Refresh at ~80% of the lifetime, never sooner than 30s.
      var delayMs = Math.max(Math.floor(ttl * 0.8), 30) * 1000;
      refreshTimer = setTimeout(function () {
        loadToken(true).then(function (tok) {
          if (tok) provideToken();
        }).catch(function () { /* keep last token; next interaction retries */ });
      }, delayMs);
    }

    // Returns a Promise resolving to a usable token. In static mode this is the
    // embedded publish token. In secure mode it fetches from tokenEndpoint.
    function loadToken(force) {
      if (staticToken) return Promise.resolve(staticToken);
      if (currentToken && !force) return Promise.resolve(currentToken);
      if (tokenInFlight) return tokenInFlight;
      tokenInFlight = fetch(tokenEndpoint, {
        method: 'GET',
        credentials: 'include',
        headers: { Accept: 'application/json' },
      })
        .then(function (res) {
          if (!res.ok) throw new Error('token endpoint HTTP ' + res.status);
          return res.json();
        })
        .then(function (data) {
          var d = data || {};
          var inner = d.data || d;
          var tok = inner.token || inner.session_token || '';
          var expiresIn = inner.expiresIn || inner.expires_in || 0;
          if (!tok) throw new Error('token endpoint returned no token');
          currentToken = tok;
          scheduleRefresh(expiresIn);
          return tok;
        })
        .catch(function (e) {
          console.error('[WeKnora] failed to load token', e);
          throw e;
        })
        .then(function (tok) { tokenInFlight = null; return tok; }, function (e) { tokenInFlight = null; throw e; });
      return tokenInFlight;
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
    var embedUrl = baseUrl + '/embed/' + encodeURIComponent(channelId);
    var embedOrigin = baseUrl;
    try {
      // Derive the exact origin (scheme + host + port) rather than trusting the
      // raw baseUrl string, so postMessage origin checks are precise.
      embedOrigin = new URL(embedUrl, global.location ? global.location.href : undefined).origin;
    } catch (e) {
      embedOrigin = baseUrl;
    }
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
    var hostOrigin = '';
    try {
      hostOrigin = global.location ? global.location.origin : '';
    } catch (e) { hostOrigin = ''; }
    var crossOriginEmbed = hostOrigin && embedOrigin && hostOrigin !== embedOrigin;
    var sandboxAttr = String(opts.sandbox || opts.sandboxMode || '').trim();
    if (!sandboxAttr && opts.scriptEl) {
      sandboxAttr = String(opts.scriptEl.getAttribute('data-sandbox') || '').trim();
    }
    if (sandboxAttr === 'true' || sandboxAttr === '1') {
      sandboxAttr = 'allow-scripts allow-forms allow-popups allow-modals allow-same-origin';
    }
    if (!sandboxAttr && crossOriginEmbed) {
      sandboxAttr = 'allow-scripts allow-forms allow-popups allow-modals allow-same-origin';
    }
    if (sandboxAttr && sandboxAttr !== 'false' && sandboxAttr !== '0') {
      iframe.setAttribute('sandbox', sandboxAttr);
    }
    iframe.setAttribute('title', title);
    panel.appendChild(iframe);

    function isTrustedOrigin(origin) {
      if (!origin || origin === 'null') return false;
      return origin === embedOrigin;
    }

    function postToIframe(message) {
      if (!iframe.contentWindow) return;
      // Always target the known embed origin; never fall back to '*' so the
      // publish token can't leak to an unexpected document.
      iframe.contentWindow.postMessage(message, embedOrigin || '/');
    }

    function provideToken() {
      loadToken(false).then(function (tok) {
        if (!tok) return;
        postToIframe({
          source: HOST_SOURCE,
          type: 'provide_token',
          token: tok,
          channel_id: channelId,
        });
      }).catch(function () { /* already logged; iframe stays awaiting */ });
    }

    function postHostPayload(type, payload) {
      if (!iframe.contentWindow) {
        console.warn('[WeKnora] iframe not ready');
        return false;
      }
      postToIframe({ source: HOST_SOURCE, type: type, payload: payload || {} });
      return true;
    }

    function whenIframeReady(fn, attempt) {
      var tries = attempt || 0;
      if (iframeReady && iframe.contentWindow) {
        fn();
        return;
      }
      if (tries >= 20) {
        console.warn('[WeKnora] iframe not ready');
        return;
      }
      setTimeout(function () { whenIframeReady(fn, tries + 1); }, 100);
    }

    function setContext(ctx) {
      if (!ctx || typeof ctx !== 'object') {
        console.warn('[WeKnora] setContext expects an object');
        return;
      }
      postHostPayload('set_context', ctx);
    }

    function openWithQuery(query) {
      var text = String(query || '').trim();
      if (!text) {
        console.warn('[WeKnora] openWithQuery requires a non-empty query');
        return;
      }
      setOpen(true);
      whenIframeReady(function () {
        postHostPayload('open_with_query', { query: text });
      });
    }

    function setLocale(locale) {
      var loc = String(locale || '').trim();
      if (!loc) {
        console.warn('[WeKnora] setLocale requires a locale string');
        return;
      }
      postHostPayload('set_locale', { locale: loc });
    }

    function onMessage(e) {
      // Only trust messages coming from our own iframe window and origin.
      if (e.source !== iframe.contentWindow) return;
      if (!isTrustedOrigin(e.origin)) return;
      if (!e.data || e.data.source !== EMBED_SOURCE) return;
      if (e.data.channel_id && e.data.channel_id !== channelId) return;

      if (!iframeOrigin) {
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
      if (refreshTimer) clearTimeout(refreshTimer);
      global.removeEventListener('message', onMessage);
      if (launcher.parentNode) launcher.parentNode.removeChild(launcher);
      if (panel.parentNode) panel.parentNode.removeChild(panel);
      listeners = {};
      if (instance === api) instance = null;
    }

    launcher.addEventListener('click', toggle);
    iframe.addEventListener('load', function () {
      // The embed page lives at embedOrigin; we cannot (and need not) read the
      // cross-origin contentWindow.location. Just (re)provide the token.
      iframeOrigin = embedOrigin;
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
      setContext: setContext,
      openWithQuery: openWithQuery,
      setLocale: setLocale,
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
    setContext: function (ctx) { if (instance) instance.setContext(ctx); },
    openWithQuery: function (query) { if (instance) instance.openWithQuery(query); },
    setLocale: function (locale) { if (instance) instance.setLocale(locale); },
  };

  global.WeKnora = api;

  var legacyScript = document.currentScript;
  if (legacyScript) {
    var legacyChannel = legacyScript.getAttribute('data-channel');
    var legacyToken = legacyScript.getAttribute('data-token');
    var legacyTokenEndpoint = legacyScript.getAttribute('data-token-endpoint');
    if (legacyChannel && (legacyToken || legacyTokenEndpoint)) {
      api.init({
        channel: legacyChannel,
        token: legacyToken,
        tokenEndpoint: legacyTokenEndpoint,
        scriptEl: legacyScript,
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
