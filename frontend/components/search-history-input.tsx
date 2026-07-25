import { useState, type ReactNode } from "react"
import { Cloud, History, Loader2, Save, Search, Trash2, X } from "lucide-react"
import { Input } from "@/components/ui/input"
import { Popover, PopoverAnchor, PopoverContent } from "@/components/ui/popover"
import { cn } from "@/lib/utils"

export type SearchHistoryCloudItem = {
  id: number
  value: string
}

type SearchHistoryInputProps = {
  value: string
  onChange: (value: string) => void
  onClear: () => void
  onSubmit: () => void
  onHistorySelect: (value: string) => void
  placeholder: string
  history?: string[]
  cloudItems?: SearchHistoryCloudItem[]
  canSaveCloud?: boolean
  cloudBusy?: boolean
  onSaveCloud?: (value: string) => void
  onRemoveCloud?: (item: SearchHistoryCloudItem) => void
  onRemoveHistory?: (value: string) => void
}

export function SearchHistoryInput({
  value,
  onChange,
  onClear,
  onSubmit,
  onHistorySelect,
  placeholder,
  history = [],
  cloudItems = [],
  canSaveCloud = false,
  cloudBusy = false,
  onSaveCloud,
  onRemoveCloud,
  onRemoveHistory,
}: SearchHistoryInputProps) {
  const [open, setOpen] = useState(false)
  const cloudKeys = new Set(cloudItems.map((item) => normalizeSuggestion(item.value)))
  const localItems = history.filter((item) => !cloudKeys.has(normalizeSuggestion(item)))
  const trimmedValue = value.trim()
  const canSaveCurrent = canSaveCloud
    && trimmedValue !== ""
    && !cloudKeys.has(normalizeSuggestion(trimmedValue))
    && Boolean(onSaveCloud)
  const showHistory = open && (cloudItems.length > 0 || localItems.length > 0 || canSaveCurrent)

  return (
    <Popover open={showHistory} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        <div className="relative">
          <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={value}
            onChange={(event) => onChange(event.target.value)}
            onKeyDown={(event) => {
              if (event.key !== "Enter") return
              event.preventDefault()
              setOpen(false)
              onSubmit()
            }}
            onFocus={() => setOpen(true)}
            onClick={() => setOpen(true)}
            className="pl-9 pr-10"
            placeholder={placeholder}
            autoComplete="off"
          />
          {value.trim() ? (
            <button
              type="button"
              onClick={() => {
                onClear()
                setOpen(false)
              }}
              className="absolute right-2 top-1/2 inline-flex size-7 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground transition hover:bg-muted hover:text-foreground"
              aria-label="清除"
            >
              <X className="size-4" />
            </button>
          ) : null}
        </div>
      </PopoverAnchor>
      <PopoverContent
        align="start"
        sideOffset={6}
        className="w-[var(--radix-popover-trigger-width)] p-1"
        onOpenAutoFocus={(event) => event.preventDefault()}
      >
        {canSaveCurrent ? (
          <button
            type="button"
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => {
              onSaveCloud?.(trimmedValue)
              setOpen(false)
            }}
            disabled={cloudBusy}
            className="mb-1 flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm transition hover:bg-accent hover:text-accent-foreground disabled:cursor-wait disabled:opacity-60"
            title="保存到云端"
          >
            {cloudBusy ? <Loader2 className="size-3.5 animate-spin" /> : <Save className="size-3.5" />}
            <span className="min-w-0 flex-1 truncate">{`保存 "${trimmedValue}" 到云端`}</span>
          </button>
        ) : null}
        <div className="max-h-64 overflow-y-auto">
          <SuggestionSection title="云端保存" icon={<Cloud className="size-3.5" />} emptyHidden>
            {cloudItems.map((item) => (
              <SuggestionRow
                key={`cloud-${item.id}`}
                value={item.value}
                onSelect={() => {
                  onChange(item.value)
                  setOpen(false)
                  onHistorySelect(item.value)
                }}
                onRemove={onRemoveCloud ? () => onRemoveCloud(item) : undefined}
                removeLabel="移除云端保存"
              />
            ))}
          </SuggestionSection>
          <SuggestionSection title="本地历史" icon={<History className="size-3.5" />} emptyHidden>
            {localItems.map((item, index) => (
              <SuggestionRow
                key={`local-${item}-${index}`}
                value={item}
                onSelect={() => {
                  onChange(item)
                  setOpen(false)
                  onHistorySelect(item)
                }}
                onRemove={onRemoveHistory ? () => onRemoveHistory(item) : undefined}
                removeLabel="移除本地历史"
              />
            ))}
          </SuggestionSection>
        </div>
      </PopoverContent>
    </Popover>
  )
}

function SuggestionSection({
  title,
  icon,
  children,
  emptyHidden,
}: {
  title: string
  icon: ReactNode
  children: ReactNode
  emptyHidden?: boolean
}) {
  const hasChildren = Boolean(children) && (!Array.isArray(children) || children.length > 0)
  if (!hasChildren && emptyHidden) return null
  return (
    <div className="py-1">
      <div className="flex items-center gap-1.5 px-2 py-1 text-[11px] font-medium text-muted-foreground">
        {icon}
        {title}
      </div>
      {children}
    </div>
  )
}

function SuggestionRow({
  value,
  onSelect,
  onRemove,
  removeLabel,
}: {
  value: string
  onSelect: () => void
  onRemove?: () => void
  removeLabel: string
}) {
  return (
    <div className="group flex items-center gap-1 rounded-sm transition hover:bg-accent hover:text-accent-foreground">
      <button
        type="button"
        onMouseDown={(event) => event.preventDefault()}
        onClick={onSelect}
        className={cn(
          "min-w-0 flex-1 truncate px-2 py-1.5 text-left text-sm",
          onRemove ? "pr-0" : "",
        )}
        title={value}
      >
        {value}
      </button>
      {onRemove ? (
        <button
          type="button"
          onMouseDown={(event) => event.preventDefault()}
          onClick={(event) => {
            event.stopPropagation()
            onRemove()
          }}
          className="mr-1 inline-flex size-7 shrink-0 items-center justify-center rounded-sm text-muted-foreground opacity-80 transition hover:bg-background/70 hover:text-destructive sm:opacity-0 sm:group-hover:opacity-100"
          aria-label={removeLabel}
          title={removeLabel}
        >
          <Trash2 className="size-3.5" />
        </button>
      ) : null}
    </div>
  )
}

function normalizeSuggestion(value: string) {
  return value.trim().toLocaleLowerCase()
}
