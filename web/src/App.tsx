import { Navigate, Route, Routes } from 'react-router-dom'

import { ProtectedRoute } from '@/components/ProtectedRoute'
import { LoginPage } from '@/features/auth/LoginPage'
import { RoomListPage } from '@/features/rooms/RoomListPage'
import { RoomMembersPage } from '@/features/rooms/RoomMembersPage'
import { ChatRoomPage } from '@/features/chat/ChatRoomPage'

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />

      <Route
        path="/rooms"
        element={
          <ProtectedRoute>
            <RoomListPage />
          </ProtectedRoute>
        }
      />
      <Route
        path="/rooms/:id"
        element={
          <ProtectedRoute>
            <ChatRoomPage />
          </ProtectedRoute>
        }
      />
      <Route
        path="/rooms/:id/members"
        element={
          <ProtectedRoute>
            <RoomMembersPage />
          </ProtectedRoute>
        }
      />

      <Route path="/" element={<Navigate to="/rooms" replace />} />
      <Route path="*" element={<Navigate to="/rooms" replace />} />
    </Routes>
  )
}
