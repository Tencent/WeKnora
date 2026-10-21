/**
 * vue-i18n's built-in plural rule only distinguishes "one" and "other". Russian
 * has three forms (one / few / many), so messages written with three pipes need
 * a selector that follows the CLDR rules. Only locales listed here are affected;
 * everything else keeps the default rule.
 */
export const pluralRules: Record<string, (choice: number, choicesLength: number) => number> = {
  ru: russianPluralRule,
  'ru-RU': russianPluralRule,
}

function russianPluralRule(choice: number, choicesLength: number): number {
  if (choicesLength < 3) return Math.abs(choice) === 1 ? 0 : 1
  const value = Math.abs(choice)
  if (value % 10 === 1 && value % 100 !== 11) return 0
  if (value % 10 >= 2 && value % 10 <= 4 && (value % 100 < 10 || value % 100 >= 20)) return 1
  return 2
}
