import { useEffect, useRef, useState } from 'react'
import type { CSSProperties } from 'react'
import { createRoot } from 'react-dom/client'
import { ArrowUp, ChevronDown, ChevronRight, CircleAlert, FileCode2, FileText, Folder, GitBranch, GitCompareArrows, Link2, Maximize2, MessageSquare, Minimize2, Package, PanelRight, PanelRightDashed, Search, Settings, X, Zap } from 'lucide-react'
import TaskAgentChat from './TaskAgentChat'
import './style.css'

type Status = 'todo' | 'progress' | 'testing'
type DiffLine = ['meta' | 'context' | 'add' | 'remove', string]
type ChangedFile = { name: string; path: string; lines: DiffLine[] }
type Task = {
  key: string; title: string; status: Status; hasSpec: boolean; developer: string; analyst: string
  tester?: string
  summary: string; branch: string; kind: string
  dependencies: { key: string; condition: string }[]
  files: ChangedFile[]
}
type Release = { id: string; sprint: string; current: boolean; tasks: Task[] }
type Message = { id: number; text: string; createdAt: string }
type ReviewMode = 'closed' | 'split' | 'maximized'

const statusNames: Record<Status, string> = { todo: 'Не взята в работу', progress: 'В работе', testing: 'На тестировании' }
const dateFormat = new Intl.DateTimeFormat('ru', { hour: '2-digit', minute: '2-digit' })
const minSidebar = 200
const collapsedSidebarWidth = 64
const minReview = 290
const minWorkspace = 320

function usePanelWidth(panel: 'sidebar' | 'review') {
  const key = `zeussdd.width.${panel}`
  const [width, setWidth] = useState(() => {
    try {
      const saved = Number(localStorage.getItem(key))
      return Number.isFinite(saved) && saved > 0 ? saved : undefined
    } catch {
      return undefined
    }
  })
  useEffect(() => {
    if (width === undefined) return
    try { localStorage.setItem(key, String(width)) } catch { /* Storage can be disabled. */ }
  }, [key, width])
  return [width, setWidth] as const
}

function PanelDivider({ panel, width, min, max, expandedMin, onResize }: {
  panel: 'sidebar' | 'review'; width: number; min: number; max: number; expandedMin?: number; onResize: (width: number) => void
}) {
  const drag = useRef<{ pointerId: number; x: number; width: number } | null>(null)
  const direction = panel === 'sidebar' ? 1 : -1

  function resize(value: number) {
    if (expandedMin !== undefined) {
      value = value < (min + expandedMin) / 2 ? min : Math.max(expandedMin, value)
    }
    onResize(Math.round(Math.max(min, Math.min(max, value))))
  }

  return <div className="panel-divider" role="separator" tabIndex={0} aria-orientation="vertical"
    aria-label={panel === 'sidebar' ? 'Ширина списка задач' : 'Ширина панели изменений'}
    aria-controls={panel} aria-valuemin={min} aria-valuemax={max} aria-valuenow={width}
    aria-valuetext={expandedMin !== undefined && width === min ? 'Свёрнута' : undefined}
    onPointerDown={event => {
      if (event.button !== 0 || drag.current) return
      event.preventDefault()
      event.currentTarget.focus()
      event.currentTarget.setPointerCapture(event.pointerId)
      const renderedWidth = panel === 'sidebar' ? event.currentTarget.previousElementSibling?.getBoundingClientRect().width : undefined
      drag.current = { pointerId: event.pointerId, x: event.clientX, width: renderedWidth ?? width }
    }}
    onPointerMove={event => {
      const start = drag.current
      if (start?.pointerId === event.pointerId) resize(start.width + (event.clientX - start.x) * direction)
    }}
    onLostPointerCapture={event => {
      if (drag.current?.pointerId === event.pointerId) drag.current = null
    }}
    onKeyDown={event => {
      const step = event.shiftKey ? 50 : 16
      const next = { ArrowLeft: width - step * direction, ArrowRight: width + step * direction, Home: min, End: max }
      if (!Object.hasOwn(next, event.key)) return
      event.preventDefault()
      const value = next[event.key as keyof typeof next]
      if (expandedMin !== undefined && width === min && value > width) resize(event.key === 'End' ? max : expandedMin)
      else if (width === expandedMin && value < width) resize(min)
      else resize(value)
    }}
  />
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, init)
  const data = await response.json()
  if (!response.ok) throw new Error(data.error || 'Ошибка загрузки.')
  return data as T
}

function StatusDot({ status }: { status: Status }) {
  return <span className={`dot ${status}`} role="img" aria-label={statusNames[status]} title={statusNames[status]} />
}

type Theme = 'dark' | 'light'

function SettingsMenu({ compact }: { compact: boolean }) {
  const dialog = useRef<HTMLDialogElement>(null)
  const [theme, setTheme] = useState<Theme | null>(null)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    setError('')
    request<{ theme: Theme }>('/api/settings', { signal: controller.signal })
      .then(({ theme }) => setTheme(theme))
      .catch((error: Error) => { if (!controller.signal.aborted) setError(error.message) })
    return () => controller.abort()
  }, [attempt])

  useEffect(() => {
    document.documentElement.dataset.theme = theme ?? 'dark'
  }, [theme])

  async function save(value: Theme) {
    setSaving(true)
    setError('')
    try {
      const updated = await request<{ theme: Theme }>('/api/settings', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ theme: value }),
      })
      setTheme(updated.theme)
    } catch (error) {
      setError(error instanceof Error ? error.message : 'Не удалось сохранить тему.')
    } finally {
      setSaving(false)
    }
  }

  return <>
    <div className="sidebar-footer">
      <button type="button" className="settings-button" aria-haspopup="dialog" aria-label="Настройки" title={compact ? 'Настройки' : undefined} onClick={() => dialog.current?.showModal()}>
        <span className="settings-icon"><Settings size={18} aria-hidden="true" /></span>{!compact && 'Настройки'}
      </button>
    </div>
    <dialog className="settings-dialog" aria-labelledby="settings-title" ref={dialog}>
      <header><h2 id="settings-title">Настройки</h2><form method="dialog"><button className="icon-button" aria-label="Закрыть настройки"><X size={18} /></button></form></header>
      <label className="theme-setting">Тема
        <select value={theme ?? ''} disabled={theme === null || saving} onChange={event => void save(event.target.value as Theme)}>
          {theme === null && <option value="">{error ? 'Недоступна' : 'Загрузка…'}</option>}
          <option value="dark">Тёмная</option>
          <option value="light">Светлая</option>
        </select>
      </label>
      {error && <p className="inline-error" role="alert">{error} {theme === null && <button className="text-button" onClick={() => setAttempt(previous => previous + 1)}>Повторить</button>}</p>}
    </dialog>
  </>
}

function App() {
  const [releases, setReleases] = useState<Release[]>([])
  const [selected, setSelected] = useState('')
  const [opened, setOpened] = useState<string[]>([])
  const [query, setQuery] = useState('')
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [loading, setLoading] = useState(true)
  const [reviewMode, setReviewMode] = useState<ReviewMode>(() => window.innerWidth > 850 ? 'split' : 'closed')
  const [viewport, setViewport] = useState(window.innerWidth)
  const [preferredSidebar, setSidebarWidth] = usePanelWidth('sidebar')
  const [preferredReview, setReviewWidth] = usePanelWidth('review')
  const [sidebarExpansionStart, setSidebarExpansionStart] = useState<number | undefined>(undefined)
  const sidebar = useRef<HTMLElement>(null)
  const lastExpandedSidebar = useRef<number | undefined>(undefined)
  const focusReleases = useRef(false)

  useEffect(() => {
    const measure = () => setViewport(window.innerWidth)
    window.addEventListener('resize', measure)
    return () => window.removeEventListener('resize', measure)
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    request<{ releases: Release[] }>('/api/workspace', { signal: controller.signal })
      .then(({ releases }) => {
        setReleases(releases)
        const initial = releases.find(release => release.current) ?? releases[0]
        setOpened(initial ? [initial.id] : [])
        setSelected(initial?.tasks[0]?.key ?? '')
      })
      .catch((error: Error) => { if (!controller.signal.aborted) setError(error.message) })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [attempt])

  const release = releases.find(item => item.tasks.some(task => task.key === selected))
  const task = release?.tasks.find(task => task.key === selected)
  const allTasks = releases.flatMap(release => release.tasks)
  const search = query.trim().toLocaleLowerCase('ru')
  const desktop = viewport > 850
  const sidebarCollapsed = desktop && preferredSidebar === collapsedSidebarWidth
  const sidebarExpanding = desktop && sidebarExpansionStart !== undefined
  const reviewDocked = desktop && Boolean(task) && reviewMode === 'split'
  const panelSpace = viewport - minWorkspace - (reviewDocked ? 2 : 1)
  const defaultSidebar = viewport <= 1100 ? 232 : 276
  const sidebarLimit = panelSpace - (reviewDocked ? minReview : 0)
  const sidebarWidth = sidebarCollapsed ? collapsedSidebarWidth : Math.max(minSidebar, Math.min(preferredSidebar ?? defaultSidebar, sidebarLimit))
  const maxReview = Math.max(minReview, panelSpace - sidebarWidth)
  const reviewWidth = Math.max(minReview, Math.min(preferredReview ?? Math.round(Math.min(860, viewport * .37)), maxReview))
  const maxSidebar = Math.max(panelSpace - (reviewDocked ? reviewWidth : 0), sidebarCollapsed ? Math.min(defaultSidebar, sidebarLimit) : minSidebar)
  const panelStyles = desktop ? {
    '--sidebar-width': `${sidebarWidth}px`,
    '--review-width': `${reviewWidth}px`,
    '--workspace-min-width': `${minWorkspace}px`,
    '--sidebar-expansion-start': sidebarExpanding ? `${sidebarExpansionStart}px` : undefined,
  } as CSSProperties : undefined
  const reviewDivider = reviewDocked && <PanelDivider panel="review" width={reviewWidth} min={minReview} max={maxReview} onResize={setReviewWidth} />

  function resizeSidebar(width: number) {
    if (width === collapsedSidebarWidth && !sidebarCollapsed) {
      lastExpandedSidebar.current = preferredSidebar ?? sidebarWidth
      setSidebarExpansionStart(undefined)
    } else if (sidebarCollapsed && width !== collapsedSidebarWidth && window.matchMedia('(prefers-reduced-motion: no-preference)').matches) {
      setSidebarExpansionStart(sidebar.current?.getBoundingClientRect().width)
    }
    setSidebarWidth(width)
  }

  function expandSidebar(focusList = false) {
    focusReleases.current = focusList
    resizeSidebar(lastExpandedSidebar.current ?? defaultSidebar)
  }

  function selectTask(key: string) {
    const parent = releases.find(release => release.tasks.some(task => task.key === key))
    if (!parent) return
    setSelected(key)
    setOpened(previous => previous.includes(parent.id) ? previous : [...previous, parent.id])
  }

  const Brand = desktop ? 'button' : 'div'
  const sidebarToggleLabel = sidebarCollapsed ? 'Развернуть боковую панель' : 'Свернуть боковую панель'

  return <div className="app" style={panelStyles}>
    <aside className={sidebarCollapsed ? 'sidebar collapsed' : sidebarExpanding ? 'sidebar expanding' : 'sidebar'}
      id="sidebar" ref={sidebar} onAnimationEnd={event => {
        if (event.target === event.currentTarget && event.animationName === 'sidebar-expand') setSidebarExpansionStart(undefined)
      }}>
      <Brand className="brand" {...(desktop ? {
        type: 'button' as const, title: sidebarToggleLabel, 'aria-label': `ZeusSDD — ${sidebarToggleLabel}`, 'aria-expanded': !sidebarCollapsed,
        onClick: () => sidebarCollapsed ? expandSidebar() : resizeSidebar(collapsedSidebarWidth),
      } : {})}>
        <span className="brand-mark"><Zap size={19} fill="currentColor" strokeWidth={1.5} aria-hidden="true" /></span>
        {!sidebarCollapsed && <span>Zeus<span className="brand-accent">SDD</span><small>Рабочее пространство</small></span>}
      </Brand>
      {sidebarCollapsed ? <button type="button" className="icon-button releases-button" aria-label="Релизы" title="Релизы" onClick={() => expandSidebar(true)}>
        <Package size={20} aria-hidden="true" />
      </button> : <>
        <label className="search"><Search size={16} /><input value={query} onChange={event => setQuery(event.target.value)} placeholder="Поиск задач" aria-label="Поиск задач" />{query && <button className="icon-button" onClick={() => setQuery('')} aria-label="Очистить поиск"><X size={14} /></button>}</label>
        <nav className="folders" aria-label="Релизы" tabIndex={-1} ref={list => {
          if (!list || !focusReleases.current) return
          focusReleases.current = false
          list.focus({ preventScroll: true })
        }}>
          <div className="section-label">Релизы</div>
          {releases.map(release => {
            const matching = release.tasks.filter(task => `${task.key} ${task.title}`.toLocaleLowerCase('ru').includes(search))
            const expanded = opened.includes(release.id) || Boolean(search)
            if (search && !matching.length) return null
            return <div className="folder" key={release.id}>
              {search ? <div className="folder-button"><Folder size={17} /><span>{release.id}</span></div> : <button className="folder-button" onClick={() => setOpened(previous => expanded ? previous.filter(id => id !== release.id) : [...previous, release.id])} aria-expanded={expanded}>
                {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}<Folder size={17} /><span>{release.id}</span>{release.current && <span className="current">Текущий</span>}
              </button>}
              {expanded && <div className="task-list">{matching.map(task => <button className={`task-button ${task.key === selected ? 'selected' : ''}`} aria-current={task.key === selected ? 'true' : undefined} key={task.key} onClick={() => selectTask(task.key)}>
                <span className="task-line"><StatusDot status={task.status} /><span>{task.key}</span>{task.hasSpec && <span className="sdd">SDD</span>}</span>
                <span className="task-title">{task.title}</span>
              </button>)}</div>}
            </div>
          })}
          {search && !allTasks.some(task => `${task.key} ${task.title}`.toLocaleLowerCase('ru').includes(search)) && <p className="sidebar-empty">Задачи не найдены</p>}
        </nav>
      </>}
      <SettingsMenu compact={sidebarCollapsed} />
    </aside>
    {desktop && <PanelDivider panel="sidebar" width={sidebarWidth} min={collapsedSidebarWidth} max={maxSidebar} expandedMin={minSidebar} onResize={resizeSidebar} />}
    {task && release ? <TaskWorkspace key={task.key} {...{ task, release, allTasks, selectTask, reviewMode, setReviewMode, reviewDivider }} /> :
      <main className="welcome">
        {error ? <><CircleAlert size={24} /><p role="alert">{error}</p><button className="secondary" onClick={() => setAttempt(value => value + 1)}>Повторить</button></> : <p>{loading ? 'Загружаем релизы…' : 'Выберите задачу в списке.'}</p>}
      </main>}
  </div>
}

function TaskWorkspace({ task, release, allTasks, selectTask, reviewMode, setReviewMode, reviewDivider }: {
  task: Task; release: Release; allTasks: Task[]; selectTask: (key: string) => void
  reviewMode: ReviewMode; setReviewMode: (value: ReviewMode) => void
  reviewDivider: React.ReactNode
}) {
  const [messages, setMessages] = useState<Message[]>([])
  const [draft, setDraft] = useState('')
  const [loading, setLoading] = useState(true)
  const [sending, setSending] = useState(false)
  const [loadError, setLoadError] = useState('')
  const [sendError, setSendError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [fileIndex, setFileIndex] = useState(0)
  const [agentChat, setAgentChat] = useState(false)
  const bottom = useRef<HTMLDivElement>(null)
  const focusReviewToggle = useRef(false)
  const endpoint = `/api/tasks/${encodeURIComponent(task.key)}/messages`

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setLoadError('')
    request<Message[]>(endpoint, { signal: controller.signal })
      .then(setMessages)
      .catch((error: Error) => { if (!controller.signal.aborted) setLoadError(error.message) })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [endpoint, attempt])

  useEffect(() => {
    if (messages.length) bottom.current?.scrollIntoView({ block: 'nearest' })
  }, [messages])

  async function send() {
    const text = draft.trim()
    if (!text || sending || loading || loadError) return
    setSending(true)
    setSendError('')
    try {
      const message = await request<Message>(endpoint, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ text }),
      })
      setMessages(previous => [...previous, message])
      setDraft('')
    } catch (error) {
      setSendError(error instanceof Error ? error.message : 'Не удалось отправить сообщение.')
    } finally {
      setSending(false)
    }
  }

  const file = task.files[fileIndex]
  const reviewOpen = reviewMode !== 'closed'
  const reviewMaximized = reviewMode === 'maximized'
  const toggleLabel = reviewOpen ? 'Скрыть боковую панель' : 'Показать боковую панель'
  const maximizeLabel = reviewMaximized ? 'Вернуть карточку задачи' : 'Развернуть изменения'
  const PanelIcon = reviewOpen ? PanelRight : PanelRightDashed
  const MaximizeIcon = reviewMaximized ? Minimize2 : Maximize2

  function toggleReview() {
    focusReviewToggle.current = true
    setReviewMode(reviewOpen ? 'closed' : 'split')
  }

  const reviewToggle = <button type="button" className="icon-button review-toggle"
    aria-label={toggleLabel} title={toggleLabel} aria-expanded={reviewOpen}
    aria-controls={reviewOpen ? 'review' : undefined} onClick={toggleReview}
    ref={button => {
      if (!button || !focusReviewToggle.current) return
      focusReviewToggle.current = false
      button.focus({ preventScroll: true })
    }}>
    <PanelIcon size={20} aria-hidden="true" />
  </button>

  return <>
    <main className="workspace" id="workspace" hidden={reviewMaximized}>
      <div className="topbar"><span className="breadcrumb"><Folder size={16} /> <span>{release.id}</span><ChevronRight size={13} /><strong>{task.key}</strong></span>
        {!reviewOpen && reviewToggle}
      </div>
      <header className="task-header">
        <div className="task-meta"><span className="status-pill"><StatusDot status={task.status} />{statusNames[task.status]}</span><span className="kind">{task.kind}</span></div>
        <h1>{task.title}</h1>
        <div className="people">
          <span><b>Разработчик</b>{task.developer}</span>
          <span><b>Аналитик</b>{task.analyst}</span>
          <span><b>Тестировщик</b>{task.tester || 'Не назначен'}</span>
        </div>
        <span className="branch"><GitBranch size={15} />{task.branch}</span>
        {task.dependencies.map(dependency => {
          const target = allTasks.find(task => task.key === dependency.key)
          return <div className="dependency" key={dependency.key}>
            <span className="dependency-icon"><Link2 size={17} /></span>
            <div><div className="dependency-title"><strong>Блокирующая задача</strong>
              {target ? <button className="text-button" onClick={() => selectTask(target.key)}>{target.key} · {target.title}</button> : <span>{dependency.key}</span>}
            </div><p>{target && <><StatusDot status={target.status} /> {statusNames[target.status]} · {target.developer} · </>}{dependency.condition}</p></div>
          </div>
        })}
      </header>
      <div className="chat-switch" role="group" aria-label="Чат задачи">
        <button type="button" className="secondary" aria-pressed={!agentChat} onClick={() => setAgentChat(false)}>Команда</button>
        <button type="button" className="secondary" aria-pressed={agentChat} onClick={() => setAgentChat(true)}>GigaCode</button>
      </div>
      <section hidden={agentChat} className="conversation" aria-label="Чат команды">
        <article className="task-summary"><span className="summary-icon"><FileText size={20} /></span><div><h2>Описание задачи</h2><p>{task.summary}</p></div></article>
        {loading && <p className="muted">Загружаем чат…</p>}
        {loadError && <div className="inline-error" role="alert">{loadError} <button className="text-button" onClick={() => setAttempt(value => value + 1)}>Повторить</button></div>}
        {!loading && !loadError && messages.length === 0 && <div className="empty-chat"><MessageSquare size={17} /><span>Здесь появятся сообщения по задаче.</span></div>}
        {messages.map(message => <article className="message" key={message.id}><header><strong>Вы</strong><time dateTime={message.createdAt}>{dateFormat.format(new Date(message.createdAt))}</time></header><p>{message.text}</p></article>)}
        <div ref={bottom} />
      </section>
      <form hidden={agentChat} className="composer" onSubmit={event => { event.preventDefault(); void send() }}>
        {sendError && <p role="alert" className="inline-error">{sendError}</p>}
        <div className="composer-box">
          <textarea aria-label="Сообщение по задаче" placeholder="Написать сообщение по задаче…" value={draft} readOnly={sending} maxLength={4000} onChange={event => setDraft(event.target.value)} onKeyDown={event => {
            if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); void send() }
          }} />
          <div className="composer-actions"><span>Shift + Enter для новой строки</span><button className="send" aria-label="Отправить сообщение" title="Отправить сообщение" disabled={!draft.trim() || sending || loading || Boolean(loadError)}><ArrowUp size={19} /></button></div>
        </div>
      </form>
      <TaskAgentChat taskKey={task.key} active={agentChat} />
    </main>
    {reviewOpen && <>{reviewDivider}
      {!reviewMaximized && <button className="review-backdrop" aria-label="Закрыть панель изменений" tabIndex={-1} onClick={toggleReview} />}
      <aside className={reviewMaximized ? 'review maximized' : 'review'} id="review" aria-label="Изменения в задаче">
      <header className="review-header">
        <GitCompareArrows size={18} /><h2>Изменения</h2>
        <button type="button" className="icon-button review-toggle" aria-label={maximizeLabel} title={maximizeLabel}
          aria-controls="workspace" onClick={() => setReviewMode(reviewMaximized ? 'split' : 'maximized')}>
          <MaximizeIcon size={20} aria-hidden="true" />
        </button>
        {reviewToggle}
      </header>
      <div className="review-body">
        <div className="file-caption">Файлы <span>{task.files.length}</span></div>
        {task.files.length ? <div className="files">{task.files.map((file, index) => {
          const added = file.lines.filter(([type]) => type === 'add').length
          const removed = file.lines.filter(([type]) => type === 'remove').length
          const Icon = file.name === 'spec.md' ? FileText : FileCode2
          return <button className={`file ${index === fileIndex ? 'active' : ''}`} aria-pressed={index === fileIndex} key={file.path} onClick={() => setFileIndex(index)}>
            <Icon size={16} /><span>{file.name}</span><small className="added">+{added}</small>{removed > 0 && <small className="removed">−{removed}</small>}
          </button>
        })}</div> : <p className="muted">В этой задаче пока нет изменений для ревью.</p>}
        {file && <div className="diff"><div className="diff-path" title={file.path}><FileCode2 size={15} />{file.path}</div>
          <pre key={file.path} aria-label="Дифф файла"><code>
            {file.lines.map(([type, line], index) => (
              <span key={index} className={`line ${type}`}>
                <span className="marker" aria-hidden="true">{type === 'add' ? '+' : type === 'remove' ? '−' : ' '}</span>
                {line}
              </span>
            ))}
          </code></pre>
        </div>}
      </div>
    </aside></>}
  </>
}

createRoot(document.getElementById('root')!).render(<App />)
