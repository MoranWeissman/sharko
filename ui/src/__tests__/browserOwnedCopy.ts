// browserOwnedCopy — the classification list for story P5's sixth zero:
// "every remaining browser-owned sentence explicitly classified as
// presentation copy".
//
// # What this file is for
//
// Almost every sentence about a connection is authored by the server, ships on
// the wire, and is rendered by the browser verbatim. Story P5 removed the
// browser's hand-typed copies of those: shipped code that renders what it
// received keeps no copy at all, and the two places that genuinely need a
// sentence with no server response to render — a test fixture, and one action
// menu the plan never reaches — import it from
// ui/src/generated/connection-sentences.ts.
//
// What is left is a small set of words the BROWSER owns, which happen to be
// spelled the same as a sentence the server also owns. "Out of sync" is the
// obvious one: it is a server headline for a connection AND the browser's own
// word for a resource row whose live state does not match its source. Those
// are two different facts on two different surfaces that agree on a word.
//
// A collision is not a copy — but from the outside they look identical, and
// "it is fine, it is a coincidence" is exactly the sentence that let fifty-two
// real copies accumulate. So every collision is written down here, with the
// surface that owns it and why it is not a copy, and
// serverSentenceCopies.test.ts fails if this list and the tree ever disagree
// in either direction: a new collision that nobody classified, or an entry
// here whose text is no longer in the file it names.
//
// # How to use it
//
// Adding a browser-owned string that happens to equal a server sentence: add
// an entry here saying which surface owns the word and why the two facts are
// different. If you cannot write that sentence honestly, it is a copy — delete
// it and render what the server sent, or import the generated constant.
//
// Adding a copy of a server sentence: do not. There is no entry shape for it.

import type { ConnectionSentenceId } from '@/generated/connection-sentences'

export interface BrowserOwnedCopy {
  /** Project-relative path, matching import.meta.glob's keys: "/src/…". */
  file: string
  /** The exact literal in that file. */
  text: string
  /** The server sentence it collides with. Typed, so a renamed id fails the build. */
  collidesWith: ConnectionSentenceId
  /** Which surface owns this word, and why it is a different fact. */
  why: string
}

export const BROWSER_OWNED_COPY: BrowserOwnedCopy[] = [
  {
    file: '/src/components/resource/StatusMark.tsx',
    text: 'Out of sync',
    collidesWith: 'headlineOutOfSync',
    why:
      "StatusMark owns six words for a resource row's live-versus-source state, chosen from a state " +
      'identifier and never from text. Four of the six (Synced, Missing, Not in config, Managed elsewhere) ' +
      'are in no server catalog at all — they are the house vocabulary a Managed Secrets row, its dot, ' +
      'its edge strip and its filter chip all read from one table. Importing two of the six from the ' +
      'connection catalog would split one vocabulary across two owners and make the table lie about ' +
      'where its words come from.',
  },
  {
    file: '/src/components/resource/StatusMark.tsx',
    text: 'Not checked yet',
    collidesWith: 'headlineNotCheckedYet',
    why:
      'Same table, same reason. This word is also the one the browser falls back to when the server sent ' +
      'no canonical answer at all, which is precisely the case where there is no server sentence to ' +
      'render — ManagedSecrets reads it back out of this table (statusLabel) rather than typing it a ' +
      'second time.',
  },
  {
    file: '/src/views/connectionActivity.ts',
    text: 'Addon labels synced',
    collidesWith: 'headlineAddonLabelsSynced',
    why:
      'The activity feed titles an audit EVENT — cluster_secret_user_label_sync, a write that already ' +
      'happened — and every one of its two dozen siblings in that table is browser-authored ' +
      '("Connection Secret created", "Addon labels self-healed"). The server sentence is a connection ' +
      "STATE right now. Same words, different tense and different subject; the feed's table is the one " +
      'owner of its own titles.',
  },
]
