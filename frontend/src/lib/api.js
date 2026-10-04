const TOKEN_KEY = 'token'

export function getToken() {
  return localStorage.getItem(TOKEN_KEY)
}
export function getUser() {
  try {
    return JSON.parse(localStorage.getItem('user') || 'null')
  } catch {
    return null
  }
}
export function setSession(token, user) {
  localStorage.setItem(TOKEN_KEY, token)
  localStorage.setItem('user', JSON.stringify(user))
}
export function clearSession() {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem('user')
}

async function request(method, path, body) {
  const headers = {}
  const token = getToken()
  if (token) headers.Authorization = 'Bearer ' + token

  let payload
  if (body instanceof FormData) {
    payload = body // multipart，不手动设置 Content-Type
  } else if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    payload = JSON.stringify(body)
  }

  const res = await fetch('/api' + path, { method, headers, body: payload })
  if (res.status === 401) {
    clearSession()
    if (location.hash !== '#/login' && location.pathname !== '/login') {
      location.href = '/login'
    }
    throw new Error('登录已过期，请重新登录')
  }
  const text = await res.text()
  const data = text ? JSON.parse(text) : {}
  if (!res.ok) {
    const err = new Error(data.error || `请求失败 (${res.status})`)
    err.status = res.status
    throw err
  }
  return data
}

export const api = {
  get: (p) => request('GET', p),
  post: (p, b) => request('POST', p, b),
  postForm: (p, form) => request('POST', p, form),
  photoUrl: (path) => `/api/photos/${path}?token=${encodeURIComponent(getToken() || '')}`
}
