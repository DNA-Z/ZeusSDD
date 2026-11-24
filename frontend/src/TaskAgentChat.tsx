import { useEffect, useRef, useState } from 'react'
import { ArrowUp, Square } from 'lucide-react'

const sessions = '/api/acp/sessions'

type Option = { optionId: string; name: string; kind: string }
type Permission = { id: string; toolCall?: { title?: string }; options: Option[] }
type Turn = { id: string; prompt: string; answer: string; status?: string }
type Connection = { url: string; stream: EventSource }

async function call<T = void>(url: string, method: string, body?: unknown): Promise<T> {
  const response = await fetch(url, {
    method,
    ...(body === undefined ? {} : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }),
  })
  if (response.status === 204) return undefined as T
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(data?.error || 'Сервис GigaCode недоступен.')
  return data as T
}

function closeSession(url: string) {
  void fetch(url, { method: 'DELETE', keepalive: true }).catch(() => undefined)
}

export default function TaskAgentChat({ taskKey, active }: { taskKey: string; active: boolean }) {
  const connection = useRef<Connection | null>(null)
  const mounted = useRef(false)
  const sending = useRef(false)
  const bottom = useRef<HTMLDivElement>(null)
  const [turns, setTurns] = useState<Turn[]>([])
  const [permissions, setPermissions] = useState<Permission[]>([])
  const [draft, setDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const [canCancel, setCanCancel] = useState(false)
  const [error, setError] = useState('')
  const [reconnecting, setReconnecting] = useState(false)

  useEffect(() => {
    mounted.current = true
    function disconnect() {
      const current = connection.current
      if (!current) return
      connection.current = null
      current.stream.close()
      closeSession(current.url)
    }
    window.addEventListener('pagehide', disconnect)
    return () => {
      mounted.current = false
      window.removeEventListener('pagehide', disconnect)
      disconnect()
    }
  }, [])

  useEffect(() => {
    if (active && (turns.length || permissions.length)) bottom.current?.scrollIntoView({ block: 'nearest' })
  }, [active, turns, permissions])

  function finish() {
    sending.current = false
    setBusy(false)
    setCanCancel(false)
  }

  async function connect() {
    const { sessionId } = await call<{ sessionId: string }>(sessions, 'POST', { taskKey })
    const url = sessions + '/' + encodeURIComponent(sessionId)
    if (!mounted.current) {
      closeSession(url)
      return null
    }
    const stream = new EventSource(url + '/events')
    connection.current = { url, stream }
    function disconnected() {
      stream.close()
      connection.current = null
      setReconnecting(false)
      setPermissions([])
      setError('Сессия GigaCode закрыта. Следующий запрос откроет новую сессию.')
      finish()
    }
    stream.onopen = () => setReconnecting(false)
    stream.onerror = () => {
      if (stream.readyState === EventSource.CLOSED) disconnected()
      else setReconnecting(true)
    }
    stream.onmessage = event => {
      const { kind, payload } = JSON.parse(event.data)
      switch (kind) {
        case 'turn_started':
          setTurns(previous => [...previous, { id: sessionId + ':' + event.lastEventId, prompt: payload.text, answer: '' }])
          setCanCancel(true)
          break
        case 'update':
          if (payload.sessionUpdate === 'agent_message_chunk' && payload.content?.type === 'text') {
            setTurns(previous => previous.map((turn, index) =>
              index === previous.length - 1 ? { ...turn, answer: turn.answer + payload.content.text } : turn))
          }
          break
        case 'permission':
          setPermissions(previous => [...previous, payload])
          break
        case 'permission_resolved':
          setPermissions(previous => previous.filter(item => item.id !== payload.id))
          break
        case 'turn_finished': {
          const status = payload.error || ({
            cancelled: 'Ответ остановлен.',
            max_tokens: 'Достигнут лимит токенов.',
            max_turn_requests: 'Достигнут лимит запросов.',
            refusal: 'GigaCode отказался продолжать ответ.',
          } as Record<string, string>)[payload.stopReason]
          setTurns(previous => previous.map((turn, index) =>
            index === previous.length - 1 ? { ...turn, status } : turn))
          finish()
          break
        }
        case 'closed':
          disconnected()
          break
      }
    }
    return url
  }

  async function send() {
    const text = draft.trim()
    if (!text || sending.current || reconnecting) return
    sending.current = true
    setBusy(true)
    setError('')
    try {
      const url = connection.current?.url ?? await connect()
      if (!url) return
      // The stream carries turn boundaries and chunks in order.
      setDraft('')
      await call(url + '/prompt', 'POST', { text })
    } catch (failure) {
      if (!mounted.current) return
      setError(failure instanceof Error ? failure.message : 'Не удалось отправить запрос.')
      setDraft(previous => previous || text)
      finish()
    }
  }

  async function cancel() {
    const current = connection.current
    if (!current) return
    setCanCancel(false)
    try {
      await call(current.url + '/cancel', 'POST')
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'Не удалось остановить ответ.')
      setCanCancel(sending.current)
    }
  }

  async function resolve(permission: Permission, optionId?: string) {
    const current = connection.current
    if (!current) return
    try {
      await call(current.url + '/permissions', 'POST', { id: permission.id, optionId })
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'Не удалось отправить решение.')
    }
  }

  return <>
    <section hidden={!active} className="conversation agent-conversation" aria-label="Чат задачи с GigaCode">
      {turns.length === 0 && <p className="muted">Отправьте запрос GigaCode по задаче {taskKey}.</p>}
      {turns.map(turn => <div key={turn.id}>
        <article className="message"><header><strong>Вы</strong></header><p>{turn.prompt}</p></article>
        {(turn.answer || turn.status) && <article className="message">
          <header><strong>GigaCode</strong></header>
          {turn.answer && <p>{turn.answer}</p>}
          {turn.status && <p className="muted">{turn.status}</p>}
        </article>}
      </div>)}
      {permissions.map(permission => <article key={permission.id} className="message agent-permission">
        <header><strong>GigaCode запрашивает разрешение</strong></header>
        {permission.toolCall?.title && <p>{permission.toolCall.title}</p>}
        <div className="agent-actions">
          {permission.options.map(option => <button key={option.optionId} className="secondary" type="button"
            onClick={() => void resolve(permission, option.optionId)}>{option.name || option.kind}</button>)}
          <button className="secondary" type="button" onClick={() => void resolve(permission)}>Отменить запрос</button>
        </div>
      </article>)}
      <div ref={bottom} />
    </section>
    <form hidden={!active} className="composer" onSubmit={event => { event.preventDefault(); void send() }}>
      {error && <p className="inline-error" role="alert">{error}</p>}
      <div className="composer-box">
        <textarea aria-label="Запрос к GigaCode" placeholder="Спросить GigaCode о задаче…" value={draft} maxLength={32000}
          readOnly={busy} onChange={event => setDraft(event.target.value)} onKeyDown={event => {
            if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
              event.preventDefault()
              void send()
            }
          }} />
        <div className="composer-actions">
          <span role="status">{reconnecting ? 'Восстанавливаем соединение…' : busy ? 'GigaCode обрабатывает запрос…' : 'Shift + Enter для новой строки'}</span>
          {busy ? <button type="button" className="send" aria-label="Остановить ответ" title="Остановить ответ"
            disabled={!canCancel} onClick={() => void cancel()}><Square size={16} /></button> :
            <button className="send" aria-label="Отправить в GigaCode" title="Отправить в GigaCode"
              disabled={!draft.trim() || reconnecting}><ArrowUp size={19} /></button>}
        </div>
      </div>
    </form>
  </>
}
