interface Env {
  UPLOAD_GATEWAY_KEY: string
  ALLOWED_ORIGIN: string
}

interface UploadPayload {
  target: string
  expires: number
}

const aad = new TextEncoder().encode("openlist-baidu-upload-v1")
const allowedHostSuffixes = [".baidu.com", ".baidupcs.com", ".baidubce.com"]

const responseHeaders = (origin: string | null, allowedOrigin: string) => {
  const headers = new Headers({
    "Cache-Control": "no-store",
    Vary: "Origin",
  })
  if (origin === allowedOrigin) {
    headers.set("Access-Control-Allow-Origin", origin)
    headers.set("Access-Control-Allow-Methods", "PUT, OPTIONS")
    headers.set("Access-Control-Allow-Headers", "Content-Type")
    headers.set("Access-Control-Max-Age", "600")
  }
  return headers
}

const errorResponse = (
  status: number,
  message: string,
  origin: string | null,
  allowedOrigin: string,
) => {
  const headers = responseHeaders(origin, allowedOrigin)
  headers.set("Content-Type", "application/json; charset=utf-8")
  return new Response(JSON.stringify({ error: message }), { status, headers })
}

const upstreamErrorResponse = async (
  upstream: Response,
  request: Request,
  target: URL,
  origin: string | null,
  allowedOrigin: string,
) => {
  const rawBody = await upstream.text()
  let details: Record<string, unknown> = {}
  try {
    const parsed = JSON.parse(rawBody) as Record<string, unknown>
    const code = parsed.error_code ?? parsed.errno
    const message = parsed.error_msg ?? parsed.errmsg ?? parsed.error_description
    if (typeof code === "number" || typeof code === "string") {
      details.upstream_code = code
    }
    if (typeof message === "string") {
      details.upstream_message = message.slice(0, 500)
    }
  } catch {
    if (rawBody) details.upstream_message = rawBody.slice(0, 500)
  }

  const cfRequest = request as Request & { cf?: { colo?: string } }
  const diagnostic = {
    upstream_status: upstream.status,
    upstream_host: target.hostname,
    cf_colo: cfRequest.cf?.colo,
    cf_ray: request.headers.get("cf-ray") ?? undefined,
    ...details,
  }
  console.error("Baidu upload rejected", diagnostic)

  const headers = responseHeaders(origin, allowedOrigin)
  headers.set("Content-Type", "application/json; charset=utf-8")
  return new Response(
    JSON.stringify({ error: "Baidu rejected the upload part", ...diagnostic }),
    { status: upstream.status, headers },
  )
}

const decodeBase64URL = (value: string) => {
  const normalized = value.replace(/-/g, "+").replace(/_/g, "/")
  const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, "=")
  const raw = atob(padded)
  return Uint8Array.from(raw, (char) => char.charCodeAt(0))
}

const decodeHex = (value: string) => {
  if (!/^[0-9a-f]{64}$/.test(value)) {
    throw new Error("invalid gateway key")
  }
  return Uint8Array.from(value.match(/.{2}/g)!, (byte) =>
    Number.parseInt(byte, 16),
  )
}

const decryptPayload = async (token: string, keyHex: string) => {
  const encrypted = decodeBase64URL(token)
  if (encrypted.length <= 12) {
    throw new Error("invalid upload token")
  }
  const key = await crypto.subtle.importKey(
    "raw",
    decodeHex(keyHex),
    "AES-GCM",
    false,
    ["decrypt"],
  )
  const plaintext = await crypto.subtle.decrypt(
    { name: "AES-GCM", iv: encrypted.slice(0, 12), additionalData: aad },
    key,
    encrypted.slice(12),
  )
  return JSON.parse(new TextDecoder().decode(plaintext)) as UploadPayload
}

const validateTarget = (payload: UploadPayload) => {
  if (!Number.isSafeInteger(payload.expires) || payload.expires < Date.now() / 1000) {
    throw new Error("upload token expired")
  }
  const target = new URL(payload.target)
  if (
    target.protocol !== "https:" ||
    target.pathname !== "/rest/2.0/pcs/superfile2" ||
    !allowedHostSuffixes.some((suffix) => target.hostname.endsWith(suffix))
  ) {
    throw new Error("invalid upload target")
  }
  return target
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const origin = request.headers.get("Origin")
    const headers = responseHeaders(origin, env.ALLOWED_ORIGIN)
    if (request.method === "OPTIONS") {
      if (origin !== env.ALLOWED_ORIGIN) {
        return errorResponse(403, "origin not allowed", origin, env.ALLOWED_ORIGIN)
      }
      return new Response(null, { status: 204, headers })
    }

    const requestURL = new URL(request.url)
    if (request.method === "GET" && requestURL.pathname === "/health") {
      headers.set("Content-Type", "application/json; charset=utf-8")
      return new Response(JSON.stringify({ status: "ok" }), { headers })
    }
    if (request.method !== "PUT" || requestURL.pathname !== "/upload") {
      return errorResponse(404, "not found", origin, env.ALLOWED_ORIGIN)
    }
    if (origin && origin !== env.ALLOWED_ORIGIN) {
      return errorResponse(403, "origin not allowed", origin, env.ALLOWED_ORIGIN)
    }
    if (!request.body) {
      return errorResponse(400, "upload body is required", origin, env.ALLOWED_ORIGIN)
    }
    const contentType = request.headers.get("Content-Type")
    if (!contentType?.toLowerCase().startsWith("multipart/form-data;")) {
      return errorResponse(400, "multipart upload is required", origin, env.ALLOWED_ORIGIN)
    }

    try {
      const token = requestURL.searchParams.get("token")
      if (!token) throw new Error("upload token is required")
      const payload = await decryptPayload(token, env.UPLOAD_GATEWAY_KEY)
      const target = validateTarget(payload)
      const upstream = await fetch(target, {
        method: "POST",
        headers: { "Content-Type": contentType },
        body: request.body,
        redirect: "manual",
      })
      if (!upstream.ok) {
        return upstreamErrorResponse(
          upstream,
          request,
          target,
          origin,
          env.ALLOWED_ORIGIN,
        )
      }
      const response = new Response(upstream.body, {
        status: upstream.status,
        statusText: upstream.statusText,
        headers,
      })
      const upstreamType = upstream.headers.get("Content-Type")
      if (upstreamType) response.headers.set("Content-Type", upstreamType)
      return response
    } catch (error) {
      return errorResponse(
        400,
        error instanceof Error ? error.message : "upload failed",
        origin,
        env.ALLOWED_ORIGIN,
      )
    }
  },
}
