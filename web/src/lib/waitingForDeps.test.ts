import { describe, it, expect } from 'vitest'
import { waitingForDepsText } from './waitingForDeps'
import en from '../locales/en'
import ru from '../locales/ru'

type Params = Record<string, string | number> | undefined

// A translator over one real locale file — the point of these cases is that
// BOTH halves of the sentence come from the reader's own asset, so stubbing
// the lookups would test nothing.
function translatorFor(table: Record<string, string>) {
  const render = (key: string, params?: Params) => {
    let text = table[key] ?? key
    for (const [k, v] of Object.entries(params ?? {})) text = text.replace(`{${k}}`, String(v))
    return text
  }
  return { t: render as never, tDynamic: render }
}

const enT = translatorFor(en)
const ruT = translatorFor(ru)

describe('waitingForDepsText', () => {
  it('names the dependency and TRANSLATES its status', () => {
    const app = { waitingFor: [{ app: 'qdrant', status: 'STOPPED' }] }
    expect(waitingForDepsText(app, enT.t, enT.tDynamic)).toBe('Waiting for: qdrant (Stopped)')
  })

  it('words the whole sentence in the READER’s locale, not the daemon’s', () => {
    const app = { waitingFor: [{ app: 'qdrant', status: 'STOPPED' }] }
    // The daemon sent the same payload in both cases: the raw status constant.
    expect(waitingForDepsText(app, ruT.t, ruT.tDynamic)).toBe('Ожидает: qdrant (Остановлен)')
  })

  it('never leaks the raw status constant for a status the UI knows', () => {
    const app = { waitingFor: [{ app: 'qdrant', status: 'STARTING' }] }
    const text = waitingForDepsText(app, ruT.t, ruT.tDynamic)
    expect(text).toBe('Ожидает: qdrant (Запуск)')
    expect(text).not.toContain('STARTING')
  })

  it('lists every dependency that is holding the app', () => {
    const app = {
      waitingFor: [
        { app: 'qdrant', status: 'STOPPED' },
        { app: 'postgres', status: 'STARTING' },
      ],
    }
    expect(waitingForDepsText(app, enT.t, enT.tDynamic))
      .toBe('Waiting for: qdrant (Stopped), postgres (Starting)')
  })

  it('falls back to the raw key for a status this UI does not know, rather than dropping it', () => {
    const app = { waitingFor: [{ app: 'qdrant', status: 'TELEPORTING' }] }
    expect(waitingForDepsText(app, enT.t, enT.tDynamic))
      .toBe('Waiting for: qdrant (status.TELEPORTING)')
  })

  it('says nothing when the daemon reports no hold', () => {
    expect(waitingForDepsText({}, enT.t, enT.tDynamic)).toBeNull()
    expect(waitingForDepsText({ waitingFor: [] }, enT.t, enT.tDynamic)).toBeNull()
  })
})
