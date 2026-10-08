/**
 * Hand-written types for @novnc/novnc.
 *
 * The package ships no .d.ts. Only the surface SandboxDesktop.vue uses is
 * declared here — adding a member is cheap, and a wrong guess about a member
 * we do not call would be worse than not having it.
 *
 * `"exports"` is the string `"./core/rfb.js"`, so the only valid import
 * specifier is `@novnc/novnc`. Vite 7 rejects the deep subpath.
 */
declare module '@novnc/novnc' {
  export interface RFBCredentials {
    username?: string
    password?: string
    target?: string
  }

  export interface RFBOptions {
    /**
     * Not used by WeKnora: the desktop authenticates one hop out, on
     * websockify, and the RFB security type is None. Declared so a future
     * reader can see that leaving it out is a decision, not an omission.
     */
    credentials?: RFBCredentials
    shared?: boolean
    repeaterID?: string
    wsProtocols?: string[]
  }

  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, urlOrChannel: string | WebSocket, options?: RFBOptions)

    /** Scale the framebuffer to the container instead of scrolling it. */
    scaleViewport: boolean
    /**
     * MUST stay false. It is also the precondition for the backend relay not
     * having to parse RFB opcode 251 (SetDesktopSize); see
     * rfbClientMessageSize in internal/handler/session/sandbox_desktop_rfb.go.
     */
    resizeSession: boolean
    clipViewport: boolean
    viewOnly: boolean
    background: string
    focusOnClick: boolean

    /**
     * Push local clipboard text to the server as ClientCutText / extended
     * clipboard. Classic CutText is Latin-1; code points above 0xff become
     * '?' unless the server advertised the TigerVNC extended clipboard.
     */
    clipboardPasteFrom(text: string): void
    /**
     * Send one key. `down` omitted means press then release. Used to turn a
     * local Cmd/Ctrl+V into a Linux Ctrl+V after the clipboard has been
     * synced.
     */
    sendKey(keysym: number, code: string, down?: boolean): void

    disconnect(): void
    focus(): void
    blur(): void

    addEventListener(
      type: 'connect' | 'disconnect' | 'credentialsrequired' | 'securityfailure' | 'clipboard',
      listener: (event: CustomEvent) => void,
    ): void
    removeEventListener(
      type: 'connect' | 'disconnect' | 'credentialsrequired' | 'securityfailure' | 'clipboard',
      listener: (event: CustomEvent) => void,
    ): void
  }
}
