alter table record_ids rename column kind to record_type;
alter table record_ids rename constraint record_ids_kind_check to record_ids_record_type_check;
alter table involvements rename column record_kind to record_type;
alter table involvements rename constraint involvements_record_kind_check to involvements_record_type_check;
