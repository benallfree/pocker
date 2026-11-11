type CardProps = { title: string; tag?: string; children?: any }

export default function Card({ title, tag, children }: CardProps) {
  return (
    <div class="card">
      <h3>
        {title} {tag && <span class="badge">{tag}</span>}
      </h3>
      {children}
    </div>
  )
}
