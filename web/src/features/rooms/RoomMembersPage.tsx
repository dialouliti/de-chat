import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'

import { apiClient } from '@/api/client'
import { Layout } from '@/components/Layout'

interface RoomMember {
  room_id: string
  user_id: string
  username: string
  role: string
  joined_at: string
}

interface User {
  id: string
  username: string
}

export function RoomMembersPage() {
  const { id } = useParams<{ id: string }>()
  const queryClient = useQueryClient()
  const [addUserId, setAddUserId] = useState('')

  const { data: members, isLoading } = useQuery({
    queryKey: ['room-members', id],
    queryFn: async () => {
      const { data, error } = await apiClient.GET('/api/v1/rooms/{id}/members', {
        params: { path: { id: id! } },
      })
      if (error || !data) throw new Error('failed to load members')
      return (data as unknown as RoomMember[]) ?? []
    },
    enabled: !!id,
  })

  const { data: allUsers } = useQuery({
    // Admin-only endpoint; non-admins get 403 and we just hide the add form.
    queryKey: ['users'],
    queryFn: async () => {
      const { data, error } = await apiClient.GET('/api/v1/admin/users', {})
      if (error || !data) throw new Error('failed')
      return (data as unknown as User[]) ?? []
    },
    retry: false,
  })

  const addMember = useMutation({
    mutationFn: async (vars: { userId: string }) => {
      const { error } = await apiClient.POST('/api/v1/rooms/{id}/members', {
        params: { path: { id: id! } },
        body: { user_id: vars.userId, role: 'member' },
      })
      if (error) throw error
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['room-members', id] })
      setAddUserId('')
    },
  })

  const removeMember = useMutation({
    mutationFn: async (vars: { userId: string }) => {
      const { error } = await apiClient.DELETE('/api/v1/rooms/{id}/members/{userId}', {
        params: { path: { id: id!, userId: vars.userId } },
      })
      if (error) throw error
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['room-members', id] }),
  })

  const canManage = !!allUsers // proxy: admin sees users; owner check is server-side

  return (
    <Layout>
      <div className="mx-auto max-w-2xl px-4 py-6">
        <div className="mb-4 flex items-center gap-3">
          <Link
            to={`/rooms/${id}`}
            className="rounded px-2 py-1 text-sm text-slate-400 hover:bg-slate-800 hover:text-slate-200"
          >
            ← Room
          </Link>
          <h2 className="text-xl font-semibold text-slate-100">Members</h2>
        </div>

        {isLoading && <p className="text-sm text-slate-500">Loading…</p>}

        <ul className="space-y-2">
          {members?.map((m) => (
            <li
              key={m.user_id}
              className="flex items-center justify-between rounded-lg border border-slate-800 bg-slate-900 px-4 py-2"
            >
              <div>
                <span className="font-medium text-slate-100">{m.username}</span>
                {m.role === 'owner' && (
                  <span className="ml-2 rounded bg-amber-900/60 px-1.5 py-0.5 text-xs text-amber-200">
                    owner
                  </span>
                )}
              </div>
              {canManage && m.role !== 'owner' && (
                <button
                  onClick={() => removeMember.mutate({ userId: m.user_id })}
                  className="text-xs text-red-400 hover:text-red-300"
                >
                  Remove
                </button>
              )}
            </li>
          ))}
        </ul>

        {canManage && allUsers && (
          <div className="mt-6 rounded-lg border border-slate-800 bg-slate-900 p-4">
            <h3 className="mb-2 text-sm font-medium text-slate-200">Add member</h3>
            <div className="flex gap-2">
              <select
                value={addUserId}
                onChange={(e) => setAddUserId(e.target.value)}
                className="flex-1 rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 focus:border-brand focus:outline-none"
              >
                <option value="">Select a user…</option>
                {allUsers
                  .filter((u) => !members?.some((m) => m.user_id === u.id))
                  .map((u) => (
                    <option key={u.id} value={u.id}>
                      {u.username}
                    </option>
                  ))}
              </select>
              <button
                onClick={() => addUserId && addMember.mutate({ userId: addUserId })}
                disabled={!addUserId || addMember.isPending}
                className="rounded-lg bg-brand px-4 py-2 text-sm font-medium text-white hover:bg-brand-dark disabled:opacity-50"
              >
                Add
              </button>
            </div>
            {addMember.isError && (
              <p className="mt-2 text-xs text-red-400">Failed to add member.</p>
            )}
          </div>
        )}
      </div>
    </Layout>
  )
}
