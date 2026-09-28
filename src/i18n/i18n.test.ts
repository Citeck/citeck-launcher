import { describe, expect, it } from 'vitest';
import { ru } from './ru';
import { en } from './en';

function shape(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(shape);
  if (v && typeof v === 'object') return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, shape(x)]));
  return typeof v;
}

describe('dictionaries', () => {
  it('have the same shape, including array lengths', () => expect(shape(en)).toEqual(shape(ru)));
  it('translate every sentence (no Russian text left in EN)', () => {
    const strings: string[] = [];
    const walk = (v: unknown) => {
      if (typeof v === 'string') strings.push(v);
      else if (v && typeof v === 'object') Object.values(v).forEach(walk);
    };
    // The language switch names Russian in Russian, on purpose.
    walk({ ...en, nav: { ...en.nav, langName: '' } });
    expect(strings.filter((s) => /[А-Яа-яЁё]/.test(s))).toEqual([]);
  });
});

describe('claims match the launcher README', () => {
  for (const [name, d] of [['ru', ru], ['en', en]] as const) {
    it(`${name}: the server wizard generates the admin password, it does not ask for one`, () => {
      const step = d.tracks.server.steps[2];
      expect(step).not.toMatch(/пароль —|and a password/);
      expect(step).toMatch(/пароль администратора|admin password/);
    });
    it(`${name}: backups are on demand, not automatic and not timed`, () => {
      expect(d.hero.lead).not.toMatch(/и делает бэкапы|backs them up —/);
      expect(d.hero.lead).toMatch(/по вашей команде|when you ask/);
      expect(JSON.stringify(d.features)).not.toMatch(/за минуту|in a minute/i);
    });
    it(`${name}: tracks warn that the first run takes a while`, () => {
      expect(d.tracks.firstRun).toMatch(/10\u2060?–\u2060?15/);
      expect(d.tracks.firstRun).toMatch(/ГБ|GB/);
    });
  }
});

describe('FAQ', () => {
  it('answers whether docker compose is needed, in both languages', () => {
    expect(ru.faq.items.some((i) => /docker compose/.test(i.q))).toBe(true);
    expect(en.faq.items.some((i) => /docker compose/.test(i.q))).toBe(true);
  });
});

describe('Docker', () => {
  for (const [name, d] of [['ru', ru], ['en', en]] as const) {
    it(`${name}: any Docker engine will do, Docker Desktop is only one option`, () => {
      for (const s of [d.tracks.desktop.steps[0], d.req.items[0].text]) expect(s).toMatch(/Rancher Desktop.*Colima|Colima.*Rancher Desktop/);
    });
  }
});

describe('editions comparison', () => {
  it('links to the comparison page of the docs in the page language', () => {
    expect(ru.links.editions).toBe('https://citeck-ecos.readthedocs.io/ru/latest/introduction/modules.html#community-enterprise');
    expect(en.links.editions).toBe('https://citeck-ecos.readthedocs.io/en/latest/introduction/modules.html#community-enterprise');
  });
});
