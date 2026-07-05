import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'

import { apiClient } from '@/api/client'
import { Layout } from '@/components/Layout'
import { formatTime } from '@/lib/format'

interface Room {
  id: string
  name: string
  description: string
  created_by: string
  created_at: string
}

export function RoomListPage() {
  // rooms are cached for 30s (query default) and refetched on focus-restore
  // only if stale. New rooms created via the admin client will appear after a
  // refresh.
  const { data: rooms, isLoading, error } = useQuery({
    queryKey: ['rooms'],
    queryFn: async () => {
      const { data, error } = await apiClient.GET('/api/v1/rooms', {})
      if (error || !data) throw new Error('failed to load rooms')
      return (data as unknown as Room[]) ?? []
    },
  })

  return (
    <Layout>
      <div className="mx-auto max-w-2xl px-4 py-6">
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-xl font-semibold text-slate-100">Rooms</h2>
          <span className="text-xs text-slate-500">{rooms?.length ?? 0} rooms</span>
        </div>

        {isLoading && <p className="text-sm text-slate-500">Loading…</p>}
        {error && (
          <p className="rounded-md bg-red-950/50 px-3 py-2 text-sm text-red-300">
            {(error as Error).message}
          </p>
        )}

        <ul className="space-y-2">
          {rooms?.map((room) => (
            <li key={room.id}>
              <Link
                to={`/rooms/${room.id}`}
                className="block rounded-lg border border-slate-800 bg-slate-900 p-4 transition hover:border-slate-700 hover:bg-slate-800"
              >
                <div className="flex items-baseline justify-between">
                  <span className="font-medium text-slate-100">{room.name}</span>
                  <span className="text-xs text-slate-500">
                    {formatTime(room.created_at)}
                  </span>
                </div>
                {room.description && (
                  <p className="mt-1 text-sm text-slate-400">{room.description}</p>
                )}
              </Link>
            </li>
          ))}
        </ul>

        {rooms && rooms.length === 0 && (
          <p className="mt-8 text-center text-sm text-slate-500">
            No rooms yet. An admin can create one with{' '}
            <code className="rounded bg-slate-800 px-1 py-0.5 text-xs">dechat-admin</code>.
          </p>
        )}
      </div>
    </Layout>
  )
}
