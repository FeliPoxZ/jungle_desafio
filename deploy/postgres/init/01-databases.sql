-- =============================================================================
-- Inicialização do PostgreSQL local.
-- Executado UMA ÚNICA VEZ, quando o volume de dados está vazio.
-- Se precisar rodar de novo: docker compose down --volumes (ou make clean).
--
-- Aqui garantimos os bancos/roles que o docker-compose espera encontrar:
--   jungleg   -> banco da aplicação (owner: jungleg)
--   jungle_test -> banco isolado para os testes de integração
--   keycloak  -> banco do IdP
-- =============================================================================

-- Banco isolado dos testes de integração: eles truncam tabelas entre cenários,
-- então nunca devem apontar para o banco de desenvolvimento.
SELECT 'CREATE DATABASE jungle_test OWNER jungleg'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'jungle_test')\gexec

-- Banco do Keycloak (o schema é criado/migrado pelo próprio Keycloak no boot).
SELECT 'CREATE ROLE keycloak LOGIN PASSWORD ''keycloak'''
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'keycloak')\gexec

SELECT 'CREATE DATABASE keycloak OWNER keycloak'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'keycloak')\gexec
