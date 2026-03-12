-- Schema Database untuk Super Boltz Puzzle (Multiplayer)

-- 1. Buat tabel user profile (bisa di-link ke auth.users nanti jika menggunakan Supabase Auth)
CREATE TABLE IF NOT EXISTS public.users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  username VARCHAR(50) UNIQUE NOT NULL,
  avatar_url TEXT,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- 2. Buat tabel untuk Room / Match
CREATE TABLE IF NOT EXISTS public.matches (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  room_code VARCHAR(10) NOT NULL,
  status VARCHAR(20) DEFAULT 'waiting' CHECK (status IN ('waiting', 'playing', 'finished', 'abandoned')),
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- 3. Buat tabel untuk Leaderboard In-Lobby / Room (Pemain di dalam Room)
CREATE TABLE IF NOT EXISTS public.match_players (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  match_id UUID NOT NULL REFERENCES public.matches(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
  score_time_ms INTEGER DEFAULT NULL, -- Waktu selesaikan puzzle (kecil = lebih baik), null = belum selesai
  score INTEGER DEFAULT NULL,         -- Calculated points (100000 - score_time_ms), higher = better
  is_winner BOOLEAN DEFAULT FALSE,
  joined_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
  UNIQUE(match_id, user_id) -- 1 user hanya bisa join 1x di room yang sama
);

-- --- Security & Policies ---
-- Karena Golang Server kita yang akan menjadi Host Autoritatif dan menulis ke database admin (service role key), 
-- maka sementara kita tidak perlu RLS (Row Level Security) ketat dari sisi client, kecuali jika client butuh 
-- langsung get leaderboard dari database. Tentu, RLS disarankan jika client langsung hit ke DB.

-- Mengizinkan Read Access secara anonim untuk Leaderboard Global (contoh):
ALTER TABLE public.matches ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.match_players ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.users ENABLE ROW LEVEL SECURITY;

CREATE POLICY "Allow public read-only matches" ON public.matches FOR SELECT USING (true);
CREATE POLICY "Allow public read-only match_players" ON public.match_players FOR SELECT USING (true);
CREATE POLICY "Allow public read-only users" ON public.users FOR SELECT USING (true);

-- Catatan: Akses INSERT/UPDATE biarkan tertutup (karena hanya diizinkan melalui server Golang dengan Service Key).
