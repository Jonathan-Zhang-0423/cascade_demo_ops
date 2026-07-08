import { Pool, type PoolClient, type PoolConfig } from "pg";

export interface DatabaseConfig {
  connectionString?: string;
  max?: number;
}

export type DatabaseExecutor = Pick<Pool | PoolClient, "query">;

export function createDatabasePool(config: DatabaseConfig = {}) {
  const connectionString = config.connectionString ?? process.env.DATABASE_URL ?? "postgres://postgres:postgres@localhost:5432/cascade_demoops";
  const poolConfig: PoolConfig = {
    connectionString,
    max: config.max ?? 10
  };
  return new Pool(poolConfig);
}

export async function closeDatabasePool(pool: Pool) {
  await pool.end();
}