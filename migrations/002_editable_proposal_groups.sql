BEGIN;

-- Itens pertencem à proposta, não ao grupo. Remover/recriar grupos durante uma
-- edição deve preservar os itens para que possam ser realocados atomicamente.
ALTER TABLE proposal_items DROP CONSTRAINT proposal_items_group_id_fkey;
ALTER TABLE proposal_items
  ADD CONSTRAINT proposal_items_group_id_fkey
  FOREIGN KEY (group_id) REFERENCES proposal_groups(id) ON DELETE SET NULL;

COMMIT;
