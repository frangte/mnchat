const COLORS = [
  'bg-indigo-500',
  'bg-emerald-500',
  'bg-amber-500',
  'bg-rose-500',
  'bg-sky-500',
  'bg-violet-500',
  'bg-teal-500',
]

function initials(name: string) {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((p) => p[0]!.toUpperCase())
    .join('')
}

export function Avatar({ id, name, size = 'md' }: { id: number; name: string; size?: 'sm' | 'md' | 'lg' }) {
  const dims = { sm: 'size-6 text-[10px]', md: 'size-9 text-sm', lg: 'size-11 text-base' }[size]
  return (
    <div
      aria-hidden
      className={`${dims} ${COLORS[id % COLORS.length]} flex shrink-0 items-center justify-center rounded-lg font-semibold text-white`}
    >
      {initials(name)}
    </div>
  )
}
