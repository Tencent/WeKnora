// Hold-to-talk shortcut: hold Alt/Option + Space to record, release to stop.
// Matched on `code` because Option+Space reports a non-breaking space as `key`.

export const VOICE_HOTKEY = {
  code: 'Space',
  modifier: 'altKey',
  modifierCodes: ['AltLeft', 'AltRight'],
  label: { mac: '⌥ Space', other: 'Alt+Space' },
} as const;

type HotkeyEvent = Pick<KeyboardEvent,
  'code' | 'altKey' | 'ctrlKey' | 'metaKey' | 'shiftKey' | 'repeat' | 'isComposing'>;

export function isVoiceHotkeyDown(e: HotkeyEvent): boolean {
  return e.code === VOICE_HOTKEY.code && e[VOICE_HOTKEY.modifier]
    && !e.ctrlKey && !e.metaKey && !e.shiftKey && !e.isComposing;
}

export function isVoiceHotkeyRelease(e: Pick<KeyboardEvent, 'code'>): boolean {
  return e.code === VOICE_HOTKEY.code
    || (VOICE_HOTKEY.modifierCodes as readonly string[]).includes(e.code);
}

export function voiceHotkeyLabel(platform = typeof navigator !== 'undefined' ? navigator.platform : ''): string {
  return /mac|iphone|ipad/i.test(platform) ? VOICE_HOTKEY.label.mac : VOICE_HOTKEY.label.other;
}

export interface HoldToTalk {
  keydown(e: HotkeyEvent & { preventDefault(): void }): void;
  keyup(e: Pick<KeyboardEvent, 'code'> & { preventDefault(): void }): void;
  release(): void;
  readonly holding: boolean;
}

// Tracks one press: start fires once on the first matching keydown (auto-repeat
// is swallowed), stop fires once on releasing either key or on release().
export function createHoldToTalk(handlers: {
  enabled: () => boolean;
  onStart: () => void;
  onStop: () => void;
}): HoldToTalk {
  let holding = false;
  return {
    get holding() { return holding; },
    keydown(e) {
      if (!isVoiceHotkeyDown(e)) return;
      if (holding) {
        e.preventDefault();
        return;
      }
      if (e.repeat || !handlers.enabled()) return;
      e.preventDefault();
      holding = true;
      handlers.onStart();
    },
    keyup(e) {
      if (!holding || !isVoiceHotkeyRelease(e)) return;
      e.preventDefault();
      holding = false;
      handlers.onStop();
    },
    release() {
      if (!holding) return;
      holding = false;
      handlers.onStop();
    },
  };
}
