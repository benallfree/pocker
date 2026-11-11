export function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? '' : 's'}`
}

export function when(iso: string): string {
  return (iso || '').replace('T', ' ').slice(0, 16)
}
