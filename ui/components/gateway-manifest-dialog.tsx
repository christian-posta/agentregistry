"use client"

import { useState, useEffect } from "react"
import { ServerJSON } from "@/lib/admin-api"
import { generateManifests, sanitizeName } from "@/lib/gateway-manifest"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Copy, Check, Download } from "lucide-react"

interface GatewayManifestDialogProps {
  server: ServerJSON | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

function getFirstRemote(server: ServerJSON | null): { url: string; protocol: 'StreamableHTTP' | 'SSE' } | null {
  if (!server?.remotes) return null
  for (const r of server.remotes) {
    if ((r.type === 'streamable-http' || r.type === 'sse') && r.url) {
      return {
        url: r.url,
        protocol: r.type === 'sse' ? 'SSE' : 'StreamableHTTP',
      }
    }
  }
  return null
}

export function GatewayManifestDialog({ server, open, onOpenChange }: GatewayManifestDialogProps) {
  const firstRemote = getFirstRemote(server)

  const [gwName, setGwName] = useState("")
  const [gwNamespace, setGwNamespace] = useState("agentgateway-system")
  const [resNamespace, setResNamespace] = useState("agentgateway-system")
  const [gwURL, setGwURL] = useState(firstRemote?.url ?? "")
  const [gwProtocol, setGwProtocol] = useState<'StreamableHTTP' | 'SSE'>(firstRemote?.protocol ?? 'StreamableHTTP')
  const [output, setOutput] = useState("")
  const [copied, setCopied] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Reset form when server changes
  useEffect(() => {
    const remote = getFirstRemote(server)
    setGwURL(remote?.url ?? "")
    setGwProtocol(remote?.protocol ?? 'StreamableHTTP')
    setOutput("")
    setError(null)
    setCopied(false)
  }, [server])

  const handleGenerate = () => {
    if (!server) return
    setError(null)
    try {
      const yaml = generateManifests({
        serverName: server.name,
        gwName,
        gwNamespace,
        namespace: resNamespace,
        url: gwURL,
        protocol: gwProtocol,
      })
      setOutput(yaml)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(output)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // ignore
    }
  }

  const handleDownload = () => {
    const name = server ? sanitizeName(server.name) : 'gateway'
    const blob = new Blob([output], { type: 'text/yaml' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${name}-gateway.yaml`
    a.click()
    URL.revokeObjectURL(url)
  }

  const canGenerate = gwName.trim() !== '' && gwURL.trim() !== ''

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Export Gateway Manifests</DialogTitle>
          <DialogDescription>
            Generate <code>AgentgatewayBackend</code> + <code>HTTPRoute</code> YAML for{' '}
            <strong>{server?.name}</strong>.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4 mt-2">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1">
              <Label htmlFor="gw-name">Gateway Name</Label>
              <Input
                id="gw-name"
                placeholder="my-gateway"
                value={gwName}
                onChange={(e) => setGwName(e.target.value)}
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="gw-namespace">Gateway Namespace</Label>
              <Input
                id="gw-namespace"
                placeholder="agentgateway-system"
                value={gwNamespace}
                onChange={(e) => setGwNamespace(e.target.value)}
              />
            </div>
          </div>

          <div className="space-y-1">
            <Label htmlFor="res-namespace">Resource Namespace</Label>
            <Input
              id="res-namespace"
              placeholder="agentgateway-system"
              value={resNamespace}
              onChange={(e) => setResNamespace(e.target.value)}
            />
          </div>

          <div className="space-y-1">
            <Label htmlFor="gw-url">Remote URL</Label>
            <Input
              id="gw-url"
              placeholder="https://your-mcp-server/mcp"
              value={gwURL}
              onChange={(e) => setGwURL(e.target.value)}
            />
          </div>

          <div className="space-y-1">
            <Label htmlFor="gw-protocol">Protocol</Label>
            <Select value={gwProtocol} onValueChange={(v) => setGwProtocol(v as 'StreamableHTTP' | 'SSE')}>
              <SelectTrigger id="gw-protocol">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="StreamableHTTP">StreamableHTTP</SelectItem>
                <SelectItem value="SSE">SSE</SelectItem>
              </SelectContent>
            </Select>
          </div>

          {error && (
            <p className="text-sm text-destructive">{error}</p>
          )}

          <Button onClick={handleGenerate} disabled={!canGenerate} className="w-full">
            Generate Manifests
          </Button>

          {output && (
            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <Label>Generated YAML</Label>
                <div className="flex gap-2">
                  <Button variant="outline" size="sm" onClick={handleCopy} className="gap-1.5">
                    {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
                    {copied ? 'Copied!' : 'Copy'}
                  </Button>
                  <Button variant="outline" size="sm" onClick={handleDownload} className="gap-1.5">
                    <Download className="h-3.5 w-3.5" />
                    Download
                  </Button>
                </div>
              </div>
              <pre className="bg-muted p-4 rounded-lg overflow-x-auto text-xs font-mono whitespace-pre">
                {output}
              </pre>
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
