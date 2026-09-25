import { ru } from './ru';
import { en } from './en';

export type Lang = 'ru' | 'en';
export type Dict = typeof ru;

const dicts: Record<Lang, Dict> = { ru, en };

export const t = (lang: Lang): Dict => dicts[lang];
